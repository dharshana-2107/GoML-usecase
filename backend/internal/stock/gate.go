// package stock is the "admission gate" for the sale. it lives in redis so
// 100k requests can be answered in microseconds without touching postgres.
package stock

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

//go:embed reserve.lua
var reserveSrc string

//go:embed release.lua
var releaseSrc string

var (
	ErrSoldOut       = errors.New("sold out")
	ErrAlreadyBought = errors.New("user already purchased")
	ErrKeyReused     = errors.New("idempotency key belongs to another user")
	ErrNotConfigured = errors.New("sale is not configured, run the seed tool")
)

// Keys are all the redis keys for one sku. the {sku} part is a redis
// "hash tag": it keeps every key on the same node in a redis cluster,
// which lua scripts need.
type Keys struct{ Stock, Buyers, Idem, Owner, State, StartsAt string }

func KeysFor(sku string) Keys {
	p := "sale:{" + sku + "}:"
	return Keys{p + "stock", p + "buyers", p + "idem", p + "owner", p + "state", p + "starts_at"}
}

func (k Keys) script() []string { return []string{k.Stock, k.Buyers, k.Idem, k.Owner, k.State} }

// Reservation is the result of a successful reserve call.
type Reservation struct {
	ID       string
	Replayed bool // true when this was a retry of an earlier request
}

// Gate wraps the lua scripts plus two tiny in-memory caches.
type Gate struct {
	rdb       *redis.Client
	keys      Keys
	reserve   *redis.Script
	release   *redis.Script
	soldOutAt atomic.Int64 // unix nanos of the last "sold out" answer

	mu        sync.Mutex
	startsAt  time.Time
	fetchedAt time.Time
}

func NewGate(rdb *redis.Client, sku string) *Gate {
	return &Gate{
		rdb:     rdb,
		keys:    KeysFor(sku),
		reserve: redis.NewScript(reserveSrc),
		release: redis.NewScript(releaseSrc),
	}
}

// Reserve atomically takes one unit for the user.
func (g *Gate) Reserve(ctx context.Context, userID, idemKey string) (Reservation, error) {
	// once we know it is sold out, answer from memory for half a second.
	// this saves redis from ~95% of the traffic after the last unit goes.
	if time.Since(time.Unix(0, g.soldOutAt.Load())) < 500*time.Millisecond {
		return Reservation{}, ErrSoldOut
	}
	res, err := g.reserve.Run(ctx, g.rdb, g.keys.script(), userID, idemKey, uuid.NewString()).Slice()
	if err != nil {
		return Reservation{}, fmt.Errorf("reserve script: %w", err)
	}
	code, _ := res[0].(int64)
	id, _ := res[1].(string)
	switch code {
	case 1:
		return Reservation{ID: id}, nil
	case 2:
		return Reservation{ID: id, Replayed: true}, nil
	case -1:
		g.soldOutAt.Store(time.Now().UnixNano())
		return Reservation{}, ErrSoldOut
	case -2:
		return Reservation{}, ErrAlreadyBought
	default:
		return Reservation{}, ErrKeyReused
	}
}

// Release gives a pending unit back (used when the queue publish fails).
func (g *Gate) Release(ctx context.Context, userID, idemKey, reservationID string) error {
	return g.release.Run(ctx, g.rdb, g.keys.script(), userID, idemKey, reservationID).Err()
}

// Lookup returns the owner and state of a reservation ("" if unknown).
func (g *Gate) Lookup(ctx context.Context, reservationID string) (owner, state string, err error) {
	pipe := g.rdb.Pipeline()
	o := pipe.HGet(ctx, g.keys.Owner, reservationID)
	s := pipe.HGet(ctx, g.keys.State, reservationID)
	if _, err = pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return "", "", err
	}
	return o.Val(), s.Val(), nil
}

// SetState is called by the worker once postgres has the final answer.
func (g *Gate) SetState(ctx context.Context, reservationID, state string) error {
	return g.rdb.HSet(ctx, g.keys.State, reservationID, state).Err()
}

// Remaining returns how many units are left.
func (g *Gate) Remaining(ctx context.Context) (int, error) {
	n, err := g.rdb.Get(ctx, g.keys.Stock).Int()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return max(n, 0), err
}

// StartsAt returns the sale start time, cached for one second so every
// pod agrees on the time without asking redis on each request.
func (g *Gate) StartsAt(ctx context.Context) (time.Time, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if time.Since(g.fetchedAt) < time.Second && !g.startsAt.IsZero() {
		return g.startsAt, nil
	}
	ms, err := g.rdb.Get(ctx, g.keys.StartsAt).Int64()
	if errors.Is(err, redis.Nil) {
		return time.Time{}, ErrNotConfigured
	} else if err != nil {
		return time.Time{}, err
	}
	g.startsAt, g.fetchedAt = time.UnixMilli(ms), time.Now()
	return g.startsAt, nil
}

// Reset wipes the sale and loads fresh stock. used by the seed tool and tests.
func (g *Gate) Reset(ctx context.Context, units int, startsAt time.Time) error {
	k := g.keys
	_, err := g.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.Del(ctx, k.Buyers, k.Idem, k.Owner, k.State)
		p.Set(ctx, k.Stock, strconv.Itoa(units), 0)
		p.Set(ctx, k.StartsAt, startsAt.UnixMilli(), 0)
		return nil
	})
	g.soldOutAt.Store(0)
	return err
}
