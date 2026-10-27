package orders

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"flashsale/internal/queue"
)

// this test needs a real postgres with the migration applied.
// it is skipped when DATABASE_URL is not set.
func newTestDB(t *testing.T, total int) *pgxpool.Pool {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	_, err = db.Exec(ctx, `DELETE FROM orders WHERE sku = 'TEST-SKU'`)
	if err == nil {
		_, err = db.Exec(ctx, `INSERT INTO inventory (sku, total, sold) VALUES ('TEST-SKU', $1, 0)
			ON CONFLICT (sku) DO UPDATE SET total = $1, sold = 0`, total)
	}
	if err != nil {
		t.Fatal(err)
	}
	return db
}

// even if redis were bypassed completely and 600 orders arrived for 500
// units, the database alone must confirm exactly 500.
func TestDatabaseGuardNeverOversells(t *testing.T) {
	db := newTestDB(t, 500)
	p := NewProcessor(db, nil)
	ctx := context.Background()

	var mu sync.Mutex
	counts := map[string]int{}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 20) // like 20 worker goroutines
	for i := 0; i < 600; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			status, err := p.persist(ctx, testMsg(fmt.Sprintf("u-%d", i)))
			if err != nil {
				t.Errorf("persist: %v", err)
				return
			}
			mu.Lock()
			counts[status]++
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	var sold int
	_ = db.QueryRow(ctx, `SELECT sold FROM inventory WHERE sku = 'TEST-SKU'`).Scan(&sold)
	if counts[StatusConfirmed] != 500 || counts[StatusRejected] != 100 || sold != 500 {
		t.Fatalf("want 500 confirmed / 100 rejected / sold 500, got %v sold=%d", counts, sold)
	}
}

// the same message delivered twice must create only one order.
func TestPersistIsIdempotent(t *testing.T) {
	db := newTestDB(t, 500)
	p := NewProcessor(db, nil)
	msg := testMsg("dup-user")

	for i := 0; i < 3; i++ {
		if _, err := p.persist(context.Background(), msg); err != nil {
			t.Fatal(err)
		}
	}
	var sold int
	_ = db.QueryRow(context.Background(), `SELECT sold FROM inventory WHERE sku = 'TEST-SKU'`).Scan(&sold)
	if sold != 1 {
		t.Fatalf("expected 1 unit sold after 3 deliveries, got %d", sold)
	}
}

func testMsg(user string) queue.OrderMessage {
	return queue.OrderMessage{
		ReservationID: uuid.NewString(), UserID: user, SKU: "TEST-SKU",
		IdempotencyKey: uuid.NewString(), ReservedAt: time.Now(),
	}
}
