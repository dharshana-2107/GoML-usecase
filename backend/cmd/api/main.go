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

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"flashsale/internal/api"
	"flashsale/internal/auth"
	"flashsale/internal/config"
	"flashsale/internal/queue"
	"flashsale/internal/stock"
)

func main() {
	cfg := config.Load()
	// refuse to start with a missing or weak secret instead of running unsafely
	if len(cfg.TokenSecret) < 32 {
		log.Fatal("SALE_TOKEN_SECRET must be set to at least 32 random characters (see .env.example)")
	}
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

	// a small postgres pool, only used by register and login (never by checkout)
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("postgres config: %v", err)
	}
	poolCfg.MaxConns = 10
	db, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	defer db.Close()

	gate := stock.NewGate(rdb, cfg.SKU)
	// adds this pod's "sold out" count to redis once a second (for reports)
	go gate.RunStatsFlusher(ctx, time.Second)

	srv := api.NewServer(api.Deps{
		Gate:         gate,
		Pub:          pub,
		SaleTokens:   auth.NewSigner(cfg.TokenSecret, cfg.TokenTTL),
		Sessions:     auth.NewSessionSigner(cfg.TokenSecret, cfg.SessionTTL),
		Users:        auth.NewUserStore(db),
		SKU:          cfg.SKU,
		SecureCookie: cfg.SecureCookie,
	})
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
