// package api is the http layer. every handler does a tiny, fixed amount of
// work (a couple of redis calls at most) and never talks to postgres.
package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"

	"flashsale/internal/auth"
	"flashsale/internal/queue"
	"flashsale/internal/stock"
)

// Publisher is the small part of the queue we need (easy to fake in tests).
type Publisher interface {
	Publish(ctx context.Context, msg queue.OrderMessage) error
}

type Server struct {
	gate   *stock.Gate
	pub    Publisher
	signer auth.Signer
	sku    string
}

func NewServer(gate *stock.Gate, pub Publisher, signer auth.Signer, sku string) *Server {
	return &Server{gate: gate, pub: pub, signer: signer, sku: sku}
}

// Routes wires the endpoints and wraps them with safety middleware.
func (s *Server) Routes(maxInFlight int) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /api/time", func(w http.ResponseWriter, _ *http.Request) {
		// never cached: the browser uses it once to sync its countdown clock
		writeJSON(w, http.StatusOK, map[string]int64{"serverTime": time.Now().UnixMilli()})
	})
	mux.HandleFunc("GET /api/sale/status", s.saleStatus)
	mux.HandleFunc("POST /api/sale/token", s.saleToken)
	mux.HandleFunc("POST /api/checkout", s.checkout)
	mux.HandleFunc("GET /api/orders/{id}", s.orderStatus)
	return recoverPanics(shedLoad(maxInFlight, withTimeout(2*time.Second, mux)))
}

// saleStatus is polled by every browser. nginx caches it for 1 second, so
// even 100k pollers turn into roughly one request per second here.
func (s *Server) saleStatus(w http.ResponseWriter, r *http.Request) {
	startsAt, err := s.gate.StartsAt(r.Context())
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "sale_not_ready", err)
		return
	}
	left, err := s.gate.Remaining(r.Context())
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "redis_unavailable", err)
		return
	}
	now := time.Now()
	writeJSON(w, http.StatusOK, map[string]any{
		"sku": s.sku, "serverTime": now.UnixMilli(), "startsAt": startsAt.UnixMilli(),
		"live": !now.Before(startsAt) && left > 0, "remaining": left, "soldOut": !now.Before(startsAt) && left == 0,
	})
}

// saleToken hands a signed token to a real user who has the page open.
func (s *Server) saleToken(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("X-User-ID")
	if !auth.ValidUserID(userID) {
		writeJSON(w, http.StatusUnauthorized, errBody("not_signed_in"))
		return
	}
	token, exp := s.signer.Issue(userID, time.Now())
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "expiresAt": exp.UnixMilli()})
}

// checkout is the hot path: validate, reserve in redis, queue, answer 202.
func (s *Server) checkout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID, idemKey := r.Header.Get("X-User-ID"), r.Header.Get("Idempotency-Key")

	if !auth.ValidUserID(userID) {
		writeJSON(w, http.StatusUnauthorized, errBody("not_signed_in"))
		return
	}
	if err := s.signer.Verify(r.Header.Get("X-Sale-Token"), userID, time.Now()); err != nil {
		writeJSON(w, http.StatusForbidden, errBody("invalid_sale_token"))
		return
	}
	if _, err := uuid.Parse(idemKey); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("missing_idempotency_key"))
		return
	}
	startsAt, err := s.gate.StartsAt(ctx)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "sale_not_ready", err)
		return
	}
	if time.Now().Before(startsAt) {
		writeJSON(w, http.StatusTooEarly, map[string]any{"error": "sale_not_started", "startsAt": startsAt.UnixMilli()})
		return
	}

	res, err := s.gate.Reserve(ctx, userID, idemKey)
	switch {
	case errors.Is(err, stock.ErrSoldOut):
		writeJSON(w, http.StatusConflict, errBody("sold_out"))
		return
	case errors.Is(err, stock.ErrAlreadyBought):
		writeJSON(w, http.StatusConflict, errBody("already_purchased"))
		return
	case errors.Is(err, stock.ErrKeyReused):
		writeJSON(w, http.StatusUnprocessableEntity, errBody("idempotency_key_reused"))
		return
	case err != nil:
		fail(w, http.StatusServiceUnavailable, "try_again", err)
		return
	}

	// a replayed request was already queued the first time, so skip it.
	if !res.Replayed {
		msg := queue.OrderMessage{ReservationID: res.ID, UserID: userID, SKU: s.sku, IdempotencyKey: idemKey, ReservedAt: time.Now()}
		if err := s.pub.Publish(ctx, msg); err != nil {
			// could not queue the order: give the unit back so it isn't lost.
			// we use a fresh context because the request one may be cancelled.
			bg, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if rerr := s.gate.Release(bg, userID, idemKey, res.ID); rerr != nil {
				log.Printf("release failed for %s: %v", res.ID, rerr)
			}
			fail(w, http.StatusServiceUnavailable, "try_again", err)
			return
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"reservationId": res.ID, "status": "pending"})
}

// orderStatus lets the browser poll until the worker has saved the order.
func (s *Server) orderStatus(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("X-User-ID")
	owner, state, err := s.gate.Lookup(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "try_again", err)
		return
	}
	// hide other people's orders: unknown and "not yours" look the same
	if owner == "" || owner != userID {
		writeJSON(w, http.StatusNotFound, errBody("not_found"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reservationId": r.PathValue("id"), "status": state})
}
