// package orders turns queued reservations into real rows in postgres.
// postgres is the final source of truth: even if redis somehow let one
// extra buyer through, the conditional update below would refuse it.
package orders

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"flashsale/internal/queue"
)

const (
	StatusConfirmed = "confirmed"
	StatusRejected  = "rejected"
)

// StateStore is where we publish the final answer for the browser to read
// (the redis gate in production, a fake in tests).
type StateStore interface {
	Lookup(ctx context.Context, reservationID string) (owner, state string, err error)
	SetState(ctx context.Context, reservationID, state string) error
}

type Processor struct {
	db    *pgxpool.Pool
	state StateStore
}

func NewProcessor(db *pgxpool.Pool, state StateStore) *Processor {
	return &Processor{db: db, state: state}
}

// Handle processes one message. it is idempotent: rabbitmq may deliver the
// same message twice (for example after a worker crash) and the result is
// still exactly one order.
func (p *Processor) Handle(ctx context.Context, msg queue.OrderMessage) (string, error) {
	// if the api released this reservation (queue publish timed out),
	// there is nothing to save.
	owner, _, err := p.state.Lookup(ctx, msg.ReservationID)
	if err != nil {
		return "", err
	}
	if owner == "" {
		return "skipped", nil
	}

	status, err := p.persist(ctx, msg)
	if err != nil {
		return "", err
	}
	// tell the browser. if this fails the message is retried, and persist
	// simply returns the status it already saved.
	return status, p.state.SetState(ctx, msg.ReservationID, status)
}

// persist runs one short transaction. there is no "select ... for update"
// and no lock held while waiting on anything outside the database.
func (p *Processor) persist(ctx context.Context, msg queue.OrderMessage) (string, error) {
	var status string
	err := pgx.BeginFunc(ctx, p.db, func(tx pgx.Tx) error {
		// 1. redelivery? return the earlier result.
		err := tx.QueryRow(ctx, `SELECT status FROM orders WHERE reservation_id = $1`,
			msg.ReservationID).Scan(&status)
		if err == nil {
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		// 2. atomic conditional decrement. the check (sold < total) and the
		// write happen in one statement, so two workers can never both take
		// the last unit. the row lock lasts microseconds, not seconds.
		tag, err := tx.Exec(ctx,
			`UPDATE inventory SET sold = sold + 1, updated_at = now()
			 WHERE sku = $1 AND sold < total`, msg.SKU)
		if err != nil {
			return err
		}
		status = StatusConfirmed
		if tag.RowsAffected() == 0 {
			status = StatusRejected // sold out at the database level
		}

		// 3. record the order. the primary key on reservation_id makes a
		// parallel duplicate fail and roll back, including its update above.
		_, err = tx.Exec(ctx,
			`INSERT INTO orders (reservation_id, user_id, sku, idempotency_key, status)
			 VALUES ($1, $2, $3, $4, $5)`,
			msg.ReservationID, msg.UserID, msg.SKU, msg.IdempotencyKey, status)
		return err
	})
	if err != nil {
		return "", fmt.Errorf("persist %s: %w", msg.ReservationID, err)
	}
	return status, nil
}
