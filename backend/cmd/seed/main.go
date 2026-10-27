// the seed tool resets a sale: fresh stock in redis and postgres, empty
// order table, empty queue, and a start time.
//
//	docker compose run --rm seed                 # 500 units, starts in 60s
//	docker compose run --rm seed -start-in 0s    # start right away (ci)
package main

import (
	"context"
	"flag"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"

	"flashsale/internal/config"
	"flashsale/internal/queue"
	"flashsale/internal/stock"
)

func main() {
	units := flag.Int("stock", 500, "units to put on sale")
	startIn := flag.Duration("start-in", 60*time.Second, "how long until the sale opens")
	flag.Parse()

	cfg := config.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. postgres: reset inventory and remove old orders
	db, err := pgx.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	defer func() { _ = db.Close(ctx) }()
	if _, err := db.Exec(ctx, `DELETE FROM orders WHERE sku = $1`, cfg.SKU); err != nil {
		log.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO inventory (sku, total, sold) VALUES ($1, $2, 0)
		ON CONFLICT (sku) DO UPDATE SET total = $2, sold = 0, updated_at = now()`, cfg.SKU, *units); err != nil {
		log.Fatal(err)
	}

	// 2. rabbitmq: drop leftover messages from an earlier run
	conn, err := queue.Dial(cfg.RabbitURL)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	ch, err := conn.Channel()
	if err == nil {
		err = queue.DeclareTopology(ch)
	}
	if err == nil {
		_, err = ch.QueuePurge(queue.Queue, false)
	}
	if err != nil {
		log.Fatalf("rabbitmq: %v", err)
	}

	// 3. redis: load the stock tokens and the start time
	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	defer func() { _ = rdb.Close() }()
	startsAt := time.Now().Add(*startIn)
	if err := stock.NewGate(rdb, cfg.SKU).Reset(ctx, *units, startsAt); err != nil {
		log.Fatalf("redis: %v", err)
	}

	log.Printf("sale reset: %d units of %s, opens at %s", *units, cfg.SKU, startsAt.Format(time.RFC3339))
}
