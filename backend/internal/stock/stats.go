package stock

// counts how many buyers got each answer, so after a load test we can show
// "500 reserved, 99,500 sold out" and not just the 500 winners.
//
// the lua script counts its own answers inside redis. "sold out" answers
// given from memory (see Reserve) never reach redis, so each api pod counts
// those itself and adds them to redis about once a second, in one command.
// that keeps the counting from undoing the "save redis 95% of the traffic" trick.

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Stats is how many checkout requests got each answer.
type Stats struct {
	Reserved      int64 // got a phone
	SoldOut       int64 // turned away, no stock left
	AlreadyBought int64 // turned away, one per customer
}

// FlushStats adds the memory "sold out" count to redis.
func (g *Gate) FlushStats(ctx context.Context) error {
	n := g.memSoldOut.Swap(0)
	if n == 0 {
		return nil
	}
	if err := g.rdb.HIncrBy(ctx, g.keys.Stats, "sold_out", n).Err(); err != nil {
		g.memSoldOut.Add(n) // keep the count and try again next time
		return err
	}
	return nil
}

// RunStatsFlusher flushes every interval until ctx is cancelled,
// then one last time so nothing counted is lost on shutdown.
func (g *Gate) RunStatsFlusher(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			_ = g.FlushStats(ctx)
		case <-ctx.Done():
			last, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = g.FlushStats(last)
			cancel()
			return
		}
	}
}

// ReadStats returns the counts saved in redis.
func (g *Gate) ReadStats(ctx context.Context) (Stats, error) {
	m, err := g.rdb.HGetAll(ctx, g.keys.Stats).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return Stats{}, err
	}
	num := func(k string) int64 { n, _ := strconv.ParseInt(m[k], 10, 64); return n }
	return Stats{Reserved: num("reserved"), SoldOut: num("sold_out"), AlreadyBought: num("already_bought")}, nil
}