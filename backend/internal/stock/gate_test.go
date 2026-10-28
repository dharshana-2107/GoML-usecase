package stock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// these tests need a real redis. they are skipped when REDIS_ADDR is not set.
func newTestGate(t *testing.T) *Gate {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		t.Skip("REDIS_ADDR not set")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr, PoolSize: 200})
	t.Cleanup(func() { _ = rdb.Close() })
	g := NewGate(rdb, "TEST-"+uuid.NewString()[:8])
	if err := g.Reset(context.Background(), 500, time.Now()); err != nil {
		t.Fatal(err)
	}
	return g
}

// the most important test in the project: 5,000 buyers race for 500 units
// and exactly 500 must win. run with -race to also catch data races.
func TestReserveNeverOversells(t *testing.T) {
	g := newTestGate(t)
	ctx := context.Background()

	var won, soldOut atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 5000; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := g.Reserve(ctx, fmt.Sprintf("user-%d", i), uuid.NewString())
			switch {
			case err == nil:
				won.Add(1)
			case errors.Is(err, ErrSoldOut):
				soldOut.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}(i)
	}
	wg.Wait()

	if won.Load() != 500 {
		t.Fatalf("expected exactly 500 reservations, got %d", won.Load())
	}
		if left, _ := g.Remaining(ctx); left != 0 {
		t.Fatalf("expected 0 units left, got %d", left)
	}

	// the report counters must add up to every buyer, with none missing
	if err := g.FlushStats(ctx); err != nil {
		t.Fatal(err)
	}
	st, err := g.ReadStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Reserved != 500 || st.SoldOut != soldOut.Load() || st.Reserved+st.SoldOut != 5000 {
		t.Fatalf("stats do not add up: %+v (sold out answers: %d)", st, soldOut.Load())
	}
}


// a retry with the same idempotency key must not take a second unit.
func TestReserveIsIdempotent(t *testing.T) {
	g := newTestGate(t)
	ctx := context.Background()
	key := uuid.NewString()

	first, err := g.Reserve(ctx, "alice", key)
	if err != nil {
		t.Fatal(err)
	}
	again, err := g.Reserve(ctx, "alice", key)
	if err != nil || !again.Replayed || again.ID != first.ID {
		t.Fatalf("replay should return the same reservation, got %+v %v", again, err)
	}
	if left, _ := g.Remaining(ctx); left != 499 {
		t.Fatalf("expected 499 left, got %d", left)
	}
	if _, err := g.Reserve(ctx, "mallory", key); !errors.Is(err, ErrKeyReused) {
		t.Fatalf("another user must not reuse a key, got %v", err)
	}
}

// one phone per user, even with a fresh idempotency key.
func TestOnePerUser(t *testing.T) {
	g := newTestGate(t)
	ctx := context.Background()
	if _, err := g.Reserve(ctx, "bob", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Reserve(ctx, "bob", uuid.NewString()); !errors.Is(err, ErrAlreadyBought) {
		t.Fatalf("expected ErrAlreadyBought, got %v", err)
	}
}

// release must put the unit back exactly once.
func TestReleaseReturnsUnitOnce(t *testing.T) {
	g := newTestGate(t)
	ctx := context.Background()
	key := uuid.NewString()
	r, _ := g.Reserve(ctx, "carol", key)

	_ = g.Release(ctx, "carol", key, r.ID)
	_ = g.Release(ctx, "carol", key, r.ID)
	if left, _ := g.Remaining(ctx); left != 500 {
		t.Fatalf("expected 500 after release, got %d", left)
	}
}
