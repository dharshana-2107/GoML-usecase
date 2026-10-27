// the api binary: stateless http pods that sit behind nginx.
// run as many copies as you like; they share nothing but redis and rabbitmq.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"flashsale/internal/api"
	"flashsale/internal/auth"
	"flashsale/internal/config"
	"flashsale/internal/queue"
	"flashsale/internal/stock"
)

func main() {
	cfg := config.Load()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// short timeouts: if redis is slow we would rather answer "try again"
	// than leave the user staring at a spinner.
	rdb := redis.NewClient(&redis.Options{
		Addr:         cfg.RedisAddr,
		PoolSize:     200,
		MinIdleConns: 20, // warm connections before 8pm, no cold start
		DialTimeout:  time.Second,
		ReadTimeout:  300 * time.Millisecond,
		WriteTimeout: 300 * time.Millisecond,
	})
	defer func() { _ = rdb.Close() }()
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("redis: %v", err)
	}

	pub, err := queue.NewPublisher(cfg.RabbitURL)
	if err != nil {
		log.Fatalf("rabbitmq: %v", err)
	}
	defer pub.Close()

	srv := api.NewServer(stock.NewGate(rdb, cfg.SKU), pub, auth.NewSigner(cfg.TokenSecret, cfg.TokenTTL), cfg.SKU)
	httpSrv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           srv.Routes(cfg.MaxInFlight),
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       3 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    8 << 10,
	}

	go func() {
		log.Printf("api listening on %s", cfg.HTTPAddr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http: %v", err)
		}
	}()

	// graceful shutdown: stop taking new requests, finish the ones in flight
	<-ctx.Done()
	log.Println("shutting down api")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
}
