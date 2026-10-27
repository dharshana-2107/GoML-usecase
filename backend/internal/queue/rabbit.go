// package queue moves orders from the api to the workers through rabbitmq.
// the api only publishes; the slow database write happens later in a worker,
// outside the http request. only successful reservations (max 500) are
// ever published, so the queue never sees the 100k request spike.
package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	Exchange     = "orders"
	RoutingKey   = "order.reserved"
	Queue        = "orders.process"
	DeadExchange = "orders.dlx"
	DeadQueue    = "orders.dead"
)

// OrderMessage is what the api hands to the workers.
type OrderMessage struct {
	ReservationID  string    `json:"reservationId"`
	UserID         string    `json:"userId"`
	SKU            string    `json:"sku"`
	IdempotencyKey string    `json:"idempotencyKey"`
	ReservedAt     time.Time `json:"reservedAt"`
}

// Dial connects with retries, because rabbitmq often starts slower than us.
func Dial(url string) (*amqp.Connection, error) {
	var err error
	for attempt := 1; attempt <= 15; attempt++ {
		var conn *amqp.Connection
		if conn, err = amqp.Dial(url); err == nil {
			return conn, nil
		}
		log.Printf("rabbitmq not ready (attempt %d): %v", attempt, err)
		time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
	}
	return nil, fmt.Errorf("rabbitmq unreachable: %w", err)
}

// DeclareTopology creates the exchange, the main queue and a dead letter queue.
// it is safe to call from every pod; rabbitmq ignores repeat declarations.
func DeclareTopology(ch *amqp.Channel) error {
	steps := []func() error{
		func() error { return ch.ExchangeDeclare(DeadExchange, "fanout", true, false, false, false, nil) },
		func() error { _, err := ch.QueueDeclare(DeadQueue, true, false, false, false, nil); return err },
		func() error { return ch.QueueBind(DeadQueue, "", DeadExchange, false, nil) },
		func() error { return ch.ExchangeDeclare(Exchange, "direct", true, false, false, false, nil) },
		func() error {
			// quorum queues are replicated and survive a broker crash.
			// after 5 failed deliveries a message moves to the dead letter queue
			// instead of looping forever.
			_, err := ch.QueueDeclare(Queue, true, false, false, false, amqp.Table{
				"x-queue-type":           "quorum",
				"x-delivery-limit":       5,
				"x-dead-letter-exchange": DeadExchange,
			})
			return err
		},
		func() error { return ch.QueueBind(Queue, RoutingKey, Exchange, false, nil) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

// Publisher sends messages with "publisher confirms": we only tell the user
// "you're in" after rabbitmq has safely stored the order on disk.
type Publisher struct {
	url  string
	mu   sync.Mutex // an amqp channel is not safe for parallel use
	conn *amqp.Connection
	ch   *amqp.Channel
}

func NewPublisher(url string) (*Publisher, error) {
	p := &Publisher{url: url}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p, p.connect()
}

// connect (re)opens the connection. caller must hold p.mu.
func (p *Publisher) connect() error {
	if p.conn != nil && !p.conn.IsClosed() {
		_ = p.conn.Close()
	}
	conn, err := Dial(p.url)
	if err != nil {
		return err
	}
	ch, err := conn.Channel()
	if err == nil {
		err = ch.Confirm(false)
	}
	if err == nil {
		err = DeclareTopology(ch)
	}
	if err != nil {
		_ = conn.Close()
		return err
	}
	p.conn, p.ch = conn, ch
	return nil
}

// Publish stores one order message durably or returns an error.
func (p *Publisher) Publish(ctx context.Context, msg OrderMessage) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	p.mu.Lock()
	if p.ch == nil || p.ch.IsClosed() {
		if err := p.connect(); err != nil {
			p.mu.Unlock()
			return err
		}
	}
	confirm, err := p.ch.PublishWithDeferredConfirmWithContext(ctx, Exchange, RoutingKey, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		MessageId:    msg.ReservationID, // lets consumers spot duplicates
		Timestamp:    time.Now(),
		Body:         body,
	})
	p.mu.Unlock() // wait for the confirm outside the lock so publishes overlap
	if err != nil {
		return err
	}

	ok, err := confirm.WaitContext(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("rabbitmq rejected the message")
	}
	return nil
}

func (p *Publisher) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn != nil {
		_ = p.conn.Close()
	}
}
