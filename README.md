# Full Stack Software Engineer Assignment

This project simulates a festival flash sale where 500 smartphones become available for purchase at 8:00 PM. Around 100,000 checkout requests are expected within the first 10 seconds, creating a massive spike in traffic.

During the initial trial run, the system struggled to handle the load, resulting in slow responses, database lock timeouts, and failed API requests. More importantly, it sold 540 smartphones despite having only 500 in stock, highlighting a critical inventory management issue.

**Solution:** The system uses Redis to control access to limited stock, with a single **atomic Lua script** ensuring that only available units can be reserved. Orders are then processed asynchronously through RabbitMQ, Go workers, and PostgreSQL, keeping the checkout process fast even during traffic spikes.

|---|---|
| **Frontend** | React 18 + TypeScript + Vite |
| **Gateway** | nginx — static hosting, rate limits, load balancing, 1 s micro-cache |
| **API / Workers** | Go 1.24 — 3 stateless API replicas, 2 worker replicas × 16 goroutines |
| **Data** | Redis 7 (admission gate), RabbitMQ 4 (quorum queue + DLQ), Postgres 16 |
| **Verification** | Go race-detector tests, k6 load test, `verify-sale.sh`, GitHub Actions CI |


---

## Contents

1. [Quick start](#1-quick-start)
2. [Architecture](#2-architecture)
3. [How the evaluation framework is met](#3-how-the-evaluation-framework-is-met)
   - [3.1 Frontend strategy](#31-frontend-strategy-client-optimization)
   - [3.2 Backend & middleware architecture](#32-backend--middleware-architecture)
   - [3.3 Database consistency & concurrency control](#33-database-consistency--concurrency-control)
4. [Proving zero oversell](#4-proving-zero-oversell)
5. [Running the tests](#5-running-the-tests)
6. [API reference](#6-api-reference)
7. [Project layout](#7-project-layout)
8. [CI/CD](#8-cicd)
9. [Known limits](#9-known-limits)

---

## 1. Quick start

**Prerequisites:** Docker with Compose v2 and `openssl`. (Go 1.24 and Node 22 are only needed to run tests or the dev server outside Docker.)

```bash
git clone https://github.com/dharshana-2107/GoML-usecase.git
cd GoML-usecase

# 1. create .env with random secrets (compose refuses to start without them)
echo "SALE_TOKEN_SECRET=$(openssl rand -hex 32)"  > .env
echo "RABBITMQ_PASSWORD=$(openssl rand -hex 16)" >> .env

# 2. build and start everything: postgres, redis, rabbitmq, 3 api, 2 workers, nginx
docker compose up -d --build --wait

# 3. load 500 units; the sale opens 60 seconds from now
docker compose run --rm seed
```

Then open **http://localhost:8080**:

1. **Create an account** (any email, password of 8–72 characters).
2. Watch the countdown, which is synced to the server clock.
3. Click **Buy now** when it hits zero → *Reserving…* → *Reserved* → *Confirmed*.

To act as several buyers, use separate private/incognito windows. A second purchase from the same account is refused (`already_purchased`).

| Setups | Command |
|---|---|
| Open the sale immediately | `docker compose run --rm seed -start-in 0s` |
| Change the stock | `docker compose run --rm seed -stock 50` |
| RabbitMQ dashboard | http://localhost:15672 — user `flash`, password = `RABBITMQ_PASSWORD` from `.env` |
| Follow worker logs | `docker compose logs -f worker` |
| Stop and wipe all data | `docker compose down -v` |

---

## 2. Architecture

![System architecture](docs/architecture.png)

**Lifecycle of one checkout**

1. Browser fetches a signed **sale token** ~15 s before the start (pre-fetch), so the click is a single request.
2. nginx drops bots, rate-limits, and routes to the least-busy API.
3. The API verifies the session cookie and sale token (in memory), checks the start time, then runs `reserve.lua` in Redis — **one atomic step** that checks stock, enforces one-per-user, records the idempotency key, and decrements in the inventory.
4. Processed orders are published to RabbitMQ **with publisher confirms**; the API answers `202` in milliseconds. If publishing fails, `release.lua` returns the unit and the client gets `503 Retry-After: 1`.
5. Sold out orders get `409 sold_out` straight from Redis.
6. Workers write the order to Postgres in one short transaction, set the final state in Redis, then `ack`.
7. The browser polls `GET /api/orders/{id}` with backoff until `confirmed` or `rejected`.

---

## 3. Solutions for the Evaluation Framework

### Problem statement

| Bugs | Root cause | Fix in this repo |
|---|---|---|
| **540 sold / 500 stock** | Check-then-act race: `SELECT stock` → app checks → `UPDATE`. Many transactions read "1 left" in the gap. | Check + decrement in **one atomic Redis Lua script**; Postgres re-checks with a conditional `UPDATE` and a `CHECK` constraint. |
| **Database row locks** | Every request maintains on one inventory row. | No `SELECT … FOR UPDATE`. Postgres sees only the ~500 orders, each holding a row lock for microseconds. |
| **High latency / API timeouts** | Synchronous DB writes inside the HTTP request. | API does Redis + queue publish only; DB writes happen in the workers. Every layer has short timeouts and load-shedding. |
| **Poor UX under load** | Double clicks, blind retries, no feedback. | Throttled button, idempotency keys, explicit states for every outcome. |

### 3.1 Frontend strategy

| Technique | Implementation |
|---|---|
| **Throttling button** | Leading-edge throttle (1.5 s): first tap fires instantly, rapid taps are dropped. Backed by an in-flight ref and a disabled button. |
| **Debouncing** | Used where it belongs: tab-visibility refresh (300 ms). Debounce is deliberately *not* used on the buy button, because it delays the first click. |
| **Idempotency** | One UUID per purchase attempt, reused on every retry, so a retry can never buy twice. |
| **Retries** | Only for `429/502/503/504`; honours `Retry-After`; exponential backoff with jitter to avoid synchronized retry waves. |
| **Asset pre-fetching** | 15 s before start: fetch the sale token and pre-load the code-split success screen. Hashed assets are cached for a year. |
| **Polling vs WebSockets** | **Adaptive polling** chosen: 10 s far from start → 1 s in the last 10 s → 2 s during the sale → stops when sold out; pauses in background tabs. nginx caches status for 1 s with `proxy_cache_lock`, so 100k pollers ≈ 1 req/s to the API. WebSockets were rejected: 100k persistent connections, sticky routing and reconnect storms for data that changes only a few times. |
| **Optimistic UI** | Button switches to *Reserving…* and the stock meter drops by one instantly; rolled back on failure. |
| **Server clock sync** | Countdown uses RTT-compensated server time, not the device clock. |
| **Graceful states** | `reserving · queued · confirmed · slow · sold_out · already_purchased · rejected · error`, announced via an `aria-live` banner. |
| **Bundle size / TTI** | React 18, no UI framework, inline SVG, code splitting → **≈157 KB raw / ≈52 KB gzip**. CI enforces < 200 KB and Lighthouse TTI < 1.5 s. |

### 3.2 Backend & middleware architecture

| Concern | Implementation |
|---|---|
| **Load balancing** | nginx `least_conn` over 3 API replicas, 256 upstream keepalive connections. Failed checkouts are retried once on another replica — safe only because every checkout is idempotent. |
| **Rate limiting & bots** | User-agent bot filter → per-IP (10 r/s) and per-session (2 r/s) `limit_req` with fast `429` → HMAC sale token bound to the user → one-per-user in Lua. Login/register limited to 10/min per IP. |
| **Distributed cache: Redis inventory tokens** | Stock lives in Redis as a counter; `reserve.lua` / `release.lua` are the only code that changes it. Keys use a `{sku}` hash tag so the script works in Redis Cluster. |
| **Message queue** | RabbitMQ **quorum queue** (replicated), persistent messages, **publisher confirms** before the API says "reserved", delivery limit 5 → dead-letter queue. |
| **Workers** | Bounded goroutine pool (`WORKER_COUNT`), prefetch = pool size, DB pool sized to match, ack only after commit, graceful shutdown returns unacked messages to the queue. |
| **Stateless API** | All config from env; no local state beyond two tiny safe-to-lose caches. Any replica can be killed or added. |
| **Overload protection** | Per-request 2 s timeout, concurrency cap (`MAX_IN_FLIGHT`) that answers `503 + Retry-After` immediately instead of queuing, panic recovery, 300 ms Redis timeouts. |
| **Authentication** | bcrypt passwords; stateless HMAC session cookie (`HttpOnly`, `SameSite=Strict`), so checkout never hits the DB to identify a user. Separate derived keys for session vs sale tokens. |

### 3.3 Database consistency & concurrency control

**Pessimistic vs optimistic locking vs atomic operations**

| Approach | Behaviour on one hot row with 100k requests |
|---|---|
| **Pessimistic** (`SELECT … FOR UPDATE`) | Yes | Every buyer queues behind one lock → seconds of waiting → lock timeouts, pool exhaustion, API timeouts. This matches the trial-run symptoms. |
| **Optimistic** (`version` column, retry on conflict) | Yes | No waiting, but nearly every attempt conflicts on a single hot row → retry storm that multiplies load. Good for low contention, bad for a flash sale. |
| **Atomic conditional update** (`UPDATE … WHERE sold < total`) | Yes | Check and write in one statement; lock held for microseconds. Still one DB round trip per buyer, so it is used only for the ~500 real orders. |
| **Redis Lua script** | Yes | No locks at all, one round trip, ~100k+ ops/s per core. Used as the front gate. |

**Why Lua in Redis is safer than `SELECT` then `UPDATE` at scale**

1. **Atomic by construction.** Redis executes a script to completion before running any other command, so there is no gap between "check" and "act" — the race that produced 540 sales cannot occur.
2. **No locks → no lock timeouts.** Each script takes microseconds; nothing waits on anything.
3. **One network round trip** (`EVALSHA`) instead of two statements inside a transaction.
4. **Several rules in one step:** stock check, one-per-user, idempotency replay and decrement together. In SQL that needs a multi-statement transaction with locks.
5. **Keeps Postgres out of the spike:** ~99.5% of requests are answered by Redis.

**Defence in depth — Redis is the gate, Postgres is the truth**

```sql
-- worker, inside one short transaction
UPDATE inventory SET sold = sold + 1 WHERE sku = $1 AND sold < total;   -- 0 rows → order saved as 'rejected'
INSERT INTO orders (reservation_id, ...) VALUES (...);                  -- PK makes redelivery harmless
```

- `CONSTRAINT sold_within_total CHECK (sold <= total)` — an oversold row cannot be written, even by buggy code.
- `orders.reservation_id` primary key — a redelivered message can never create a second order.
- Partial unique index `one_phone_per_user ON orders (user_id, sku) WHERE status = 'confirmed'`.
- There is exactly one counter per store (`stock` in Redis, `sold` in Postgres), each changed only atomically. The application never reads a number, does arithmetic, and writes it back.

Files: `backend/internal/stock/reserve.lua`, `release.lua`, `backend/internal/orders/processor.go`, `db/migrations/001_init.sql`.

---

## 4. Proving zero oversell

With the stack from the quick start running:

```bash
# 1. reset the sale and open it now
docker compose run --rm seed -start-in 0s

# 2. pre-sign 20,000 test-user sessions (avoids 20k slow bcrypt logins)
docker compose run --rm -T seed -sessions 20000 > loadtest/sessions.json

# 3. 20,000 users rush 500 phones
docker compose --profile loadtest run --rm k6

# 4. check the database and Redis
./scripts/verify-sale.sh
```

The k6 run **fails** unless exactly 500 reservations succeed and no request is dropped (no timeouts, connection errors, `502` or `504`). `verify-sale.sh` then waits for the workers to drain the queue and prints:

```
| check              | expected | actual |
|--------------------|----------|--------|
| inventory.sold     | 500      | 500    |
| confirmed orders   | 500      | 500    |
| rejected by db     | 0        | 0      |
| duplicate buyers   | 0        | 0      |
| redis stock left   | 0        | 0      |
PASS: exactly 500 sold, zero oversell
```


## 5. Running the tests

```bash
cd backend
go test ./...            # unit tests; Redis/Postgres tests are skipped automatically
```

The most important tests need a real Redis and Postgres:

| Test | What it proves |
|---|---|
| `stock/TestReserveNeverOversells` | 5,000 concurrent goroutines race for 500 units through `reserve.lua` → exactly 500 win. |
| `orders/TestDatabaseGuardNeverOversells` | Redis bypassed entirely: 600 orders hit Postgres for 500 units → exactly 500 confirmed. |
| `orders/TestPersistIsIdempotent` | Redelivering the same message produces one order. |
| `stock/TestReserveIsIdempotent`, `TestOnePerUser`, `TestReleaseReturnsUnitOnce` | Retry safety, one-per-user, release can't double-return stock. |
| `auth/*`, `api/*` | Token tampering, session/sale key separation, cookie flags, login timing parity. |

To run them, start throwaway containers and apply the migrations:

```bash
docker run -d --name t-redis -p 6379:6379 redis:7-alpine
docker run -d --name t-pg -p 5432:5432 -e POSTGRES_USER=app -e POSTGRES_PASSWORD=app -e POSTGRES_DB=flashsale \
  -v "$PWD/../db/migrations:/docker-entrypoint-initdb.d:ro" postgres:16-alpine

REDIS_ADDR=localhost:6379 \
DATABASE_URL="postgres://app:app@localhost:5432/flashsale?sslmode=disable" \
go test -race -count=1 ./...

docker rm -f t-redis t-pg
```

**Frontend**

```bash
cd frontend
npm ci
npm run lint && npm run build     # type check + production build, prints bundle sizes
npm run dev                       # dev server; proxies /api to the gateway on :8080
```

---

## 6. API reference

All endpoints are served through nginx on `:8080`. Identity comes only from the signed `fs_session` cookie set at login — never from a request header. Non-browser clients must send a browser-like `User-Agent`.

| Method | Path | Notes |
|---|---|---|
| `POST` | `/api/auth/register` | `{email, password}` → sets `fs_session` cookie |
| `POST` | `/api/auth/login` | `{email, password}` → sets `fs_session` cookie. Same error for unknown email and wrong password. |
| `POST` | `/api/auth/logout` | Clears the cookie |
| `GET` | `/api/auth/me` | Who is signed in (cookie check only, no DB) |
| `GET` | `/api/time` | Server clock for the countdown |
| `GET` | `/api/sale/status` | `{sku, startsAt, live, remaining, soldOut}` — cached 1 s by nginx |
| `POST` | `/api/sale/token` | Signed, user-bound sale token (15 min) |
| `POST` | `/api/checkout` | Headers: `X-Sale-Token`, `Idempotency-Key` (UUID) |
| `GET` | `/api/orders/{id}` | `pending` → `confirmed` / `rejected` (owner only) |

**Checkout responses**

| Status | Meaning |
|---|---|
| `202` | Reserved — `{reservationId, status: "pending"}` |
| `409 sold_out` / `409 already_purchased` | Final answers |
| `425 sale_not_started` | Too early |
| `401 not_signed_in` / `403 invalid_sale_token` | Auth problems |
| `400 missing_idempotency_key` / `422 idempotency_key_reused` | Bad request |
| `429` | Rate limited (nginx) |
| `503` + `Retry-After: 1` | Busy or queue unavailable — retry with the **same** idempotency key |

---

## 7. Project layout

```
backend/
  cmd/api/            stateless HTTP API
  cmd/worker/         RabbitMQ consumer with a bounded goroutine pool
  cmd/seed/           resets a sale; signs load-test sessions
  internal/stock/     Redis gate: reserve.lua, release.lua, gate.go (+ race tests)
  internal/orders/    Postgres persistence with the conditional update (+ race tests)
  internal/queue/     RabbitMQ topology, publisher confirms
  internal/auth/      HMAC tokens, bcrypt user store
  internal/api/       handlers + middleware (timeouts, load shedding, panic recovery)
  internal/config/    env-only configuration
frontend/src/
  hooks/              useCheckout (throttle, idempotency, optimistic UI, polling)
                      useSaleStatus (adaptive polling, server clock), useAuth
  components/         SalePage, Countdown, BuyButton, StatusBanner, SuccessPanel (lazy), LoginForm
  lib/                fetch wrapper with retries; throttle / debounce / backoff
nginx/                gateway config + image that builds the React app
db/migrations/        schema with constraints
loadtest/             k6 rush scenario
scripts/              verify-sale.sh
cdn/, docker-compose.cdn.yml   optional local CDN simulation (see docs/CDN.md)
docs/                 DESIGN.md (reasoning), CDN.md
.github/workflows/    CI pipeline
```

---

## 8. CI/CD

`.github/workflows/ci.yml` runs on every push and pull request:

1. **backend** — golangci-lint, migrations applied, `go test -race` against real Redis and Postgres service containers (includes both oversell race tests).
2. **frontend** — lint, type check, build, bundle budget (< 200 KB), Lighthouse TTI (< 1.5 s).
3. **e2e** — full compose stack with fresh random secrets, k6 rush with 10,000 users on 500 units, `verify-sale.sh`; the result table is posted to the run summary.
4. **publish** — on `main`, pushes `api`, `worker` and `gateway` images to GHCR tagged with the commit SHA.


---

## 9. Known limits

This is an assessment build. Being explicit about what is **not** proven or implemented:

- **Scale:** CI proves *correctness* with 10,000–20,000 buyers on one machine (20–40× more buyers than units). The full 100k-requests-in-10-seconds *capacity* target is not demonstrated here; it needs production-sized, pre-scaled infrastructure.
- **Payment & reservation expiry:** out of scope. Production would expire unpaid reservations (e.g. 10 min) and return them with `release.lua`.
- **Redis durability:** AOF `everysec` can lose up to 1 s of writes on a crash. Postgres's conditional update and `CHECK` constraint still prevent overselling; production would add replicas or a managed Redis.
- **Bot protection:** user-agent filtering and signed tokens are a first layer only; production needs a CDN/WAF challenge.
- **Account verification:** no email verification, so one person can register several emails; logout does not revoke a copied session token before it expires.

