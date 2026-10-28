// the seed tool resets a sale: fresh stock in redis and postgres, empty
// order table, empty queue, and a start time.
//
//	docker compose run --rm seed                 # 500 units, starts in 60s
//	docker compose run --rm seed -start-in 0s    # start right away (ci)
//
// for the load test it can also print signed session cookies for fake users,
// so k6 doesn't have to run 10,000 slow password logins first:
//
//	docker compose run --rm -T seed -sessions 10000 > loadtest/sessions.json
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"

	"flashsale/internal/auth"
	"flashsale/internal/config"
	"flashsale/internal/queue"
	"flashsale/internal/stock"
)

func main() {
	units := flag.Int("stock", 500, "units to put on sale")
	startIn := flag.Duration("start-in", 60*time.Second, "how long until the sale opens")
	sessions := flag.Int("sessions", 0, "print this many load test session cookies as json and exit")
	flag.Parse()

	cfg := config.Load()
	if *sessions > 0 {
		printSessions(cfg, *sessions)
		return
	}
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

// printSessions writes a json list of session tokens for users load_0, load_1...
// they are signed with the same key as real logins, so the api accepts them.
func printSessions(cfg config.Config, n int) {
	if len(cfg.TokenSecret) < 32 {
		log.Fatal("SALE_TOKEN_SECRET must be set (same value as the api)")
	}
	signer := auth.NewSessionSigner(cfg.TokenSecret, cfg.SessionTTL)
	tokens := make([]string, n)
	for i := range tokens {
		tokens[i], _ = signer.Issue(fmt.Sprintf("load_%d", i), time.Now())
	}
	if err := json.NewEncoder(os.Stdout).Encode(tokens); err != nil {
		log.Fatal(err)
	}
	log.Printf("printed %d session tokens", n) // goes to stderr, not into the json
}
