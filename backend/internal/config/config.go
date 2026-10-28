// package config reads every setting from environment variables.
// nothing is stored on local disk, so any container can be killed or
// copied at any time. this is what makes the api "stateless".
package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds all runtime settings for the api, worker and seed tools.
type Config struct {
	HTTPAddr     string        // where the api listens
	RedisAddr    string        // redis host:port
	RabbitURL    string        // amqp connection string
	DatabaseURL  string        // postgres connection string
	SKU          string        // the product on sale
	TokenSecret  []byte        // key used to sign sale tokens
	TokenTTL     time.Duration // how long a sale token stays valid
	SessionTTL   time.Duration // how long a login lasts
	SecureCookie bool          // https-only cookies (turn on in production)
	MaxInFlight  int           // max requests one api pod works on at once
	WorkerCount  int           // parallel order processors per worker pod
}

// Load builds the config with safe local defaults.
func Load() Config {
	return Config{
		HTTPAddr:    env("HTTP_ADDR", ":8080"),
		RedisAddr:   env("REDIS_ADDR", "localhost:6379"),
		RabbitURL:   env("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"),
		DatabaseURL: env("DATABASE_URL", "postgres://app:app@localhost:5432/flashsale?sslmode=disable"),
		SKU:         env("SALE_SKU", "PHONE-1"),
		// no default on purpose: a known secret would let anyone forge tokens
		TokenSecret:  []byte(os.Getenv("SALE_TOKEN_SECRET")),
		TokenTTL:     time.Duration(envInt("SALE_TOKEN_TTL_SEC", 900)) * time.Second,
		SessionTTL:   time.Duration(envInt("SESSION_TTL_SEC", 12*3600)) * time.Second,
		SecureCookie: os.Getenv("COOKIE_SECURE") == "true",
		MaxInFlight:  envInt("MAX_IN_FLIGHT", 2000),
		WorkerCount:  envInt("WORKER_COUNT", 16),
	}
}

// env returns the variable or a fallback when it is empty.
func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// envInt is like env but parses a number.
func envInt(key string, fallback int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil && v > 0 {
		return v
	}
	return fallback
}
