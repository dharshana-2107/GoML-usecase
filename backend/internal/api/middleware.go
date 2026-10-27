package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// shedLoad caps how many requests one pod handles at the same time.
// when full it answers 503 + Retry-After right away instead of letting
// requests pile up and time out. a fast "try again" beats a slow timeout,
// and nginx retries it on another replica.
func shedLoad(max int, next http.Handler) http.Handler {
	slots := make(chan struct{}, max)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
			next.ServeHTTP(w, r)
		default:
			w.Header().Set("Retry-After", "1")
			writeJSON(w, http.StatusServiceUnavailable, errBody("busy"))
		}
	})
}

// withTimeout gives every request a deadline, so a slow redis or rabbitmq
// can never hold a goroutine (and a client) forever.
func withTimeout(d time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), d)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// recoverPanics turns a crash in one handler into a 500 instead of
// killing the whole pod.
func recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic on %s: %v", r.URL.Path, rec)
				writeJSON(w, http.StatusInternalServerError, errBody("internal_error"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// writeJSON sends a json response. responses must never be cached by
// browsers, except where nginx adds its own short cache.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func errBody(code string) map[string]string { return map[string]string{"error": code} }

// fail logs the real error but only shows the client a short code.
func fail(w http.ResponseWriter, status int, code string, err error) {
	log.Printf("%s: %v", code, err)
	if status == http.StatusServiceUnavailable {
		w.Header().Set("Retry-After", "1")
	}
	writeJSON(w, status, errBody(code))
}
