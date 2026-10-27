// the worker binary: reads reserved orders from rabbitmq and saves them to
// postgres with a bounded pool of goroutines.
package main

import (
	"context"
	"encoding/json"
	"log"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"

	"flashsale/internal/config"
	"flashsale/internal/orders"
	"flashsale/internal/queue"
	"flashsale/internal/stock"
)

func main() {
	cfg := config.Load()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// the db pool is sized to the number of goroutines. more goroutines than
	// connections would only make them wait in line for the database.
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("postgres config: %v", err)
	}
	poolCfg.MaxConns = int32(cfg.WorkerCount + 2)
	db, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	defer db.Close()

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	defer func() { _ = rdb.Close() }()

	conn, err := queue.Dial(cfg.RabbitURL)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	ch, err := conn.Channel()
	if err != nil {
		log.Fatal(err)
	}
	if err := queue.DeclareTopology(ch); err != nil {
		log.Fatal(err)
	}
	// prefetch = how many unacked messages rabbitmq gives us at once
	if err := ch.Qos(cfg.WorkerCount, 0, false); err != nil {
		log.Fatal(err)
	}
	const consumerTag = "order-worker"
	deliveries, err := ch.Consume(queue.Queue, consumerTag, false, false, false, false, nil)
	if err != nil {
		log.Fatal(err)
	}

	// if rabbitmq drops us, exit so docker / kubernetes restarts a clean pod
	closed := conn.NotifyClose(make(chan *amqp.Error, 1))
	go func() {
		if e := <-closed; e != nil {
			log.Fatalf("rabbitmq connection lost: %v", e)
		}
	}()

	proc := orders.NewProcessor(db, stock.NewGate(rdb, cfg.SKU))
	var wg sync.WaitGroup
	for i := 0; i < cfg.WorkerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for d := range deliveries {
				handle(proc, d)
			}
		}()
	}
	log.Printf("worker started with %d goroutines", cfg.WorkerCount)

	// graceful shutdown: stop receiving, let in-flight orders finish, then exit.
	// unacked messages go back to the queue for another worker.
	<-ctx.Done()
	log.Println("shutting down worker")
	_ = ch.Cancel(consumerTag, false)
	wg.Wait()
}

// handle processes one delivery and acks it only after postgres committed.
func handle(proc *orders.Processor, d amqp.Delivery) {
	var msg queue.OrderMessage
	if err := json.Unmarshal(d.Body, &msg); err != nil || msg.ReservationID == "" {
		log.Printf("bad message %s, sending to dead letter queue", d.MessageId)
		_ = d.Nack(false, false)
		return
	}

	// each message gets its own deadline, so one stuck query can't block forever
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	status, err := proc.Handle(ctx, msg)
	if err != nil {
		// requeue and try again. the quorum queue's delivery limit moves it
		// to the dead letter queue after 5 failures.
		log.Printf("order %s failed: %v", msg.ReservationID, err)
		_ = d.Nack(false, true)
		return
	}
	log.Printf("order %s -> %s", msg.ReservationID, status)
	_ = d.Ack(false)
}
