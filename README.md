# Flash Sale: 500 phones, 100k buyers, zero oversell

A full-stack re-architecture of a flash-sale checkout. The trial run oversold (540 / 500) and timed out under load; this version admits buyers atomically in Redis, persists orders asynchronously through RabbitMQ and Go workers, and uses Postgres as a final guard.

**Stack:** React 18 + Vite · nginx · Go (API + worker) · Redis (go-redis) · RabbitMQ · Postgres 16 (alpine) · k6 · GitHub Actions

See [`docs/DESIGN.md`](docs/DESIGN.md) for the reasoning, including **pessimistic vs optimistic locking** and **why Lua in Redis is safer than `SELECT` then `UPDATE`**.

## Architecture

```
React (CDN-cacheable static) ─► nginx gateway ─► Go API x3 ─► Redis Lua gate
                                  │  bot filter     │ 202 Accepted   │
                                  │  rate limits    └─► RabbitMQ ─► Go workers x2 ─► Postgres
                                  │  least_conn LB                  (quorum queue + DLQ)
                                  └─ 1 s micro-cache for /api/sale/status
```

## Requirements → how they are met → how they are verified

| Requirement | Implementation | Evidence |
|---|---|---|
| Exactly 500 sold, zero oversell | `reserve.lua` atomic gate + `UPDATE ... WHERE sold < total` + `CHECK (sold <= total)` | `gate_test.go` (5,000 goroutines), `processor_test.go` (600 orders on 500 units), `verify-sale.sh` after the k6 rush |
| No row-lock timeouts | No `SELECT FOR UPDATE`; Postgres only sees ~500 short transactions | `processor.go` |
| Decouple DB writes from HTTP | API publishes to RabbitMQ (publisher confirms), workers write | `handlers.go`, `cmd/worker` |
| Stateless clusters | Config from env only; 3 API replicas behind nginx | `docker-compose.yml` |
| Event-loop safety | Per-request timeouts, concurrency cap with fast 503, no blocking work in handlers | `middleware.go` |
| Rate-limit bad actors, block bots | nginx UA filter, per-IP + per-user `limit_req`, signed sale token, one per user | `nginx.conf`, `auth/token.go` |
| 0% dropped requests | Every path returns 202/409/429/503 quickly; idempotent retries across replicas | k6 `dropped` threshold `rate==0` |
| Prevent click spam | Leading-edge throttle + in-flight guard + disabled button + idempotency key | `useCheckout.ts` |
| Graceful loading / failed queue | State machine with optimistic UI, backoff polling, "slow" state, rollback | `useCheckout.ts`, `StatusBanner.tsx` |
| Bundle < 200 KB | React 18.3, no UI libs, code splitting → ~157 KB raw / 52 KB gzip | CI bundle budget step |
| TTI < 1.5 s | Tiny bundle, inline SVG, immutable asset caching, loading shell in HTML | CI Lighthouse assertion |

## Run it

Requirements: Docker with Compose v2. Go 1.24 and Node 22 only for local development.

```bash
# 1. one-time: create go.sum and commit it
cd backend && go mod tidy && cd ..

# 2. start everything (postgres, redis, rabbitmq, 3 api, 2 workers, nginx)
docker compose up -d --build --wait

# 3. load 500 units; the sale opens in 60 seconds
docker compose run --rm seed

# 4. open the shop (use private windows to act as different users)
open http://localhost:8080
```

RabbitMQ dashboard: http://localhost:15672 (user `flash`, password `flash`).

### Prove zero oversell

```bash
docker compose run --rm seed -start-in 0s              # open the sale now
docker compose --profile loadtest run --rm k6          # 20,000 users rush 500 phones
./scripts/verify-sale.sh                                # PASS: exactly 500 sold
```

### Tests

```bash
cd backend
go test ./...                           # unit tests (integration tests skip)
REDIS_ADDR=localhost:6379 \
DATABASE_URL=postgres://app:app@localhost:5432/flashsale?sslmode=disable \
go test -race ./...                     # with the compose stack's redis/postgres*
```

\* To run against the compose stack, expose ports `6379` and `5432` in `docker-compose.yml`, or rely on CI, which runs them against service containers.

### Frontend dev server

```bash
cd frontend && npm install && npm run dev   # proxies /api to the gateway on :8080
```

## API

All endpoints go through nginx on port 8080. Non-browser clients must send a browser-like `User-Agent` (bot filter).

| Method | Path | Notes |
|---|---|---|
| GET | `/api/time` | Server clock for the countdown |
| GET | `/api/sale/status` | `{startsAt, live, remaining, soldOut}`; cached 1 s by nginx |
| POST | `/api/sale/token` | Header `X-User-ID`; returns a signed, user-bound sale token |
| POST | `/api/checkout` | Headers `X-User-ID`, `X-Sale-Token`, `Idempotency-Key` (UUID) |
| GET | `/api/orders/{id}` | `pending` → `confirmed` / `rejected` |

Checkout responses: `202` reserved · `409 sold_out` · `409 already_purchased` · `425 sale_not_started` · `403 invalid_sale_token` · `429` rate limited · `503 + Retry-After` try again.

## Project layout

```
backend/
  cmd/api            stateless http api
  cmd/worker         rabbitmq consumer, bounded goroutine pool
  cmd/seed           resets a sale (stock, start time, queue)
  internal/stock     redis gate + reserve.lua / release.lua
  internal/orders    postgres persistence with the conditional update
  internal/queue     rabbitmq topology, publisher confirms
  internal/auth      hmac sale tokens
  internal/api       handlers + middleware (timeouts, load shedding)
frontend/src
  hooks/useCheckout  throttle, idempotency, optimistic ui, order polling
  hooks/useSaleStatus adaptive polling, server clock sync
  components/        countdown, buy button, status banner, lazy success panel
nginx/               gateway config + image (builds the react app)
db/migrations/       schema with constraints
loadtest/            k6 rush scenario
scripts/             verify-sale.sh
.github/workflows/   ci: tests → bundle + lighthouse → e2e load test → publish
```

## CI/CD

`.github/workflows/ci.yml` runs on every push and pull request:

1. **backend:** golangci-lint, then `go test -race` against real Redis and Postgres service containers.
2. **frontend:** lint, type check, build, bundle budget, Lighthouse TTI.
3. **e2e:** full compose stack, k6 rush with 10,000 users, `verify-sale.sh` (results table on the run summary page).
4. **publish:** on `main`, pushes `api`, `worker` and `gateway` images to GHCR tagged with the commit SHA.

## Honest limits

This is an assessment build. Payment, reservation expiry, real authentication, CDN/WAF bot challenges, Redis replication and full-scale (100k / 10 s) capacity testing are described in `docs/DESIGN.md` §7 but not implemented.
