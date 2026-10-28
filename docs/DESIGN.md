# Design notes

This document explains *why* the system is built the way it is. Each section maps to a requirement in the brief.

## 1. Why the old system oversold (540 / 500)

The trial code almost certainly did this:

```sql
SELECT stock FROM inventory WHERE sku = 'PHONE-1';   -- returns 1
-- application checks: stock > 0 ? yes
UPDATE inventory SET stock = stock - 1 WHERE sku = 'PHONE-1';
```

Between the `SELECT` and the `UPDATE` there is a gap of a few milliseconds. Under 100k requests, dozens of transactions read "1 left" inside that gap, all pass the check, and all write. This is a classic **check-then-act race condition**. 40 extra sales is exactly what this race produces under load.

## 2. Pessimistic vs optimistic locking vs atomic operations

| Approach | How it works | Under 100k requests on one row |
|---|---|---|
| **Pessimistic locking** (`SELECT ... FOR UPDATE`) | Lock the row first, then read, check, write, commit. Everyone else waits. | Correct, but every buyer queues behind one lock. Waits grow to seconds, then `lock_timeout` / connection pool exhaustion / API timeouts. **This caused the trial's latency and row-lock timeouts.** |
| **Optimistic locking** (`version` column) | Read freely, then `UPDATE ... WHERE version = :v`. If 0 rows changed, someone beat you: retry. | Correct, no waiting, but on a single hot row almost every attempt conflicts. 100k clients retrying in a loop multiplies load (a "retry storm"). Good for low-contention data, bad for a flash sale. |
| **Atomic conditional update** (`UPDATE ... SET sold = sold + 1 WHERE sold < total`) | The check and the write are one statement. The database evaluates the condition on the latest row version. | Correct and simple. The row lock lasts microseconds. Still one round trip to postgres per buyer, so we only use it for the ~500 real orders (see below). |
| **Redis Lua script** | Check stock, check user, decrement, record: all inside one script. | Correct, ~100k+ ops/s on one core, no locks at all. Used as the front gate. |

### Why Lua in Redis is safer than SELECT-then-UPDATE at scale

1. **Atomicity by design, not by discipline.** Redis runs commands on a single thread and never interleaves another command into a running script. There is no gap between "check" and "act", so the race in section 1 is impossible. With SQL, safety depends on every developer remembering the right isolation level or lock.
2. **No locks, so no lock timeouts.** Nobody waits on anybody. Each script takes a few microseconds and the next one runs.
3. **One network round trip.** `SELECT` + `UPDATE` is two round trips plus a transaction. The script is one `EVALSHA` call.
4. **Several rules in one step.** The script checks stock, enforces one-per-user and records the idempotency key together. Doing that safely in SQL needs a multi-statement transaction with locks.
5. **It keeps postgres out of the spike.** 99.5% of requests are answered by Redis ("sold out"); postgres only ever sees the 500 winners, at the pace the workers choose.

### Defence in depth

Redis is the *admission gate*; Postgres is the *source of truth*:

- `reserve.lua` admits at most 500 buyers.
- The worker runs `UPDATE inventory SET sold = sold + 1 WHERE sku = $1 AND sold < total`. If Redis ever lost data in a failover and admitted extra buyers, this statement refuses them (the order is saved as `rejected`).
- A `CHECK (sold <= total)` constraint makes it impossible to write an oversold row even with a bug in the code.
- `orders.reservation_id` is the primary key, so a redelivered message can never create a second order, and a partial unique index allows one confirmed phone per user.

**Avoiding "application-level double counters":** there is exactly one counter in each store (`stock` in Redis, `sold` in Postgres), and each is only changed by atomic operations. The application never reads a number, does arithmetic, and writes it back.

## 3. Request lifecycle

```
browser ──► nginx ──► api (x3) ──► redis lua (reserve) ──► rabbitmq ──► worker (x2) ──► postgres
            │          │                                                  │
            │          └─ 202 Accepted {reservationId}  ◄─────────────────┘ sets state in redis
            └─ browser polls GET /api/orders/{id} until "confirmed"
```

1. **nginx** drops bot user agents, applies per-IP and per-user rate limits (fast `429`), and spreads requests across API replicas with `least_conn`.
2. **API** checks the signed sale token, the idempotency key and the start time, runs the Lua script, publishes the order with **publisher confirms**, and answers `202` in a few milliseconds. No database call.
3. If publishing fails, the API runs `release.lua` to return the unit, then answers `503 Retry-After: 1`. The client retries with the same idempotency key.
4. **Workers** consume with a bounded goroutine pool (`prefetch = WORKER_COUNT`), save the order in one short transaction, set the final state in Redis, then `ack`. A crash before `ack` means RabbitMQ redelivers; the primary key makes that harmless.
5. Poison messages go to a **dead-letter queue** after 5 deliveries (quorum queue `x-delivery-limit`).

## 4. Gateway and load balancing

- **`least_conn`** rather than round robin: a replica that is briefly slower gets fewer new requests.
- **Upstream keepalive** (256 connections): no new TCP handshake per request between nginx and Go.
- **Safe retries:** `POST /api/checkout` is retried on another replica on `502/503`/timeouts (`non_idempotent`). This is only safe because every checkout carries an idempotency key; a retry can never buy twice.
- **"0% dropped requests"** is interpreted as: every request gets a definitive answer (`202`, `409`, `429`, `503 + Retry-After`), none time out or are silently dropped. Every layer has a short timeout and returns quickly when it is full (API load shedding with a semaphore, nginx `limit_req` with `nodelay`).
- **Bot blocking** is layered: cheap user-agent filter in nginx, then the HMAC sale token (only issued to a signed-in user who loaded the page), then per-user rate limits and one-per-user in Lua. In production, add a CDN/WAF bot challenge in front.

## 5. Application logic: stateless and event-loop safe

- API pods keep **no local state** except two tiny caches (sold-out flag for 500 ms, start time for 1 s) that are safe to lose. Any pod can be killed or added.
- Go has no single event loop; each request runs in its own goroutine across all cores. We still apply the *spirit* of event-loop safety: no blocking CPU work in handlers, every call has a timeout (`context.WithTimeout`, Redis 300 ms read timeout), and a concurrency cap so a slow dependency causes fast `503`s instead of a pile of stuck goroutines.
- Heavy writes are **decoupled** from HTTP through RabbitMQ.

## 6. Frontend strategy

| Technique | Where | Why |
|---|---|---|
| **Throttle (leading edge)** | `useCheckout.ts` | First tap fires instantly; taps within 1.5 s are dropped. Plus an `inFlight` ref and a disabled button. |
| **Debounce** | `useSaleStatus.ts` | Tab-focus refresh. Debounce is *wrong* for a buy button because it delays the first click until clicking stops. |
| **Idempotency key** | `useCheckout.ts` | Generated once per attempt and sent on every retry, so network retries are safe. |
| **Retries with jittered backoff** | `api.ts` | Only for `429/502/503/504`, honours `Retry-After`, random jitter prevents synchronized retry waves. |
| **Asset pre-fetching** | `useCheckout.prepare()` | 15 s before start: fetch the sale token and pre-load the lazy success-screen chunk, so the click is a single request. Hashed assets are cached for a year by nginx. |
| **Optimistic UI** | `useCheckout` / `App.tsx` | Button shows "Reserving…" and the stock meter drops by one immediately; rolled back on failure. |
| **Adaptive polling vs WebSockets** | `useSaleStatus.ts` | See below. |
| **Server clock sync** | `useServerClock` | Countdown follows server time (RTT-compensated), not the device clock. |

**Polling vs WebSockets.** WebSockets push updates instantly, but 100k persistent connections cost memory on every gateway and API node, need sticky routing, and reconnect storms are painful when a node restarts. The sale status only changes a handful of times, so we poll with an **adaptive interval** (10 s far from start, 1 s in the final 10 s, 2 s during the sale, stop when sold out, pause in background tabs). nginx **micro-caches** `/api/sale/status` for 1 s with `proxy_cache_lock`, so 100k pollers cost the API about one request per second. SSE would be a reasonable middle ground if second-level freshness ever mattered.

**Bundle and TTI.** React 18.3 (smaller than 19), no UI framework, hand-written CSS, an inline SVG instead of images, and a code-split success screen. Build output is about 157 KB raw / 52 KB gzipped, enforced in CI along with a Lighthouse TTI assertion.

## 7. Authentication

Email and password login, built so it never slows down the sale.

```
before the sale:  browser ──login──► api ──► postgres (bcrypt check) ──► Set-Cookie: fs_session=<signed token>
during the sale:  browser ──cookie──► api ──► hmac check in memory (no database) ──► redis lua gate
```

| Decision | Why |
|---|---|
| **bcrypt (cost 10)** for passwords | Deliberately slow (~70 ms), so a leaked database can't be brute forced quickly. |
| **Signed, stateless session cookie** instead of a sessions table | Checking it is an HMAC calculation in memory. Checkout stays free of database calls, exactly as before login existed. |
| **`HttpOnly`, `SameSite=Strict`, `Secure` in production** | Scripts can't steal it (XSS), other sites can't make the browser send it (CSRF), and it never travels over plain HTTP. |
| **Separate key for sessions and sale tokens** | Both are derived from one secret, but a sale token can't be replayed as a login cookie or the other way around. |
| **Same error and timing for unknown email and wrong password** | A dummy bcrypt check runs for unknown emails, so attackers can't discover which emails are registered. |
| **Limited hashing slots per pod** (`runtime.NumCPU()`) | A burst of logins waits its turn instead of using every CPU and slowing checkout on the same pod. |
| **Strict rate limit on login/register** (10 per minute per IP, in nginx) | Makes password guessing slow and keeps hashing load bounded. |
| **`/api/auth/me` reads only the cookie** | Every visitor loads the page at 8 PM; this check must not touch the database. |

**Load testing with login.** Running 20,000 bcrypt logins would take minutes and test the wrong thing. The seed tool signs session cookies for the test users with the same key the API uses (`seed -sessions N`), so k6 measures the sale itself. Login is covered by its own tests.

## 8. Known limits and production next steps

- **Payment** is out of scope. In production a reservation would expire (e.g. 10 minutes) and a sweeper would run `release.lua` for unpaid ones.
- **Redis durability:** AOF `everysec` can lose up to one second of writes on a crash. The Postgres guard prevents overselling in that case; for stronger guarantees use Redis with replicas plus `WAIT`, or a managed service.
- **Account abuse:** login proves who you are, but one person can still register several emails. Production adds email or phone verification and limits per payment card or address.
- **Session revocation:** sessions are stateless HMAC tokens, so logout only deletes the cookie. To cancel stolen sessions, store a per-user session version in Redis and check it (one fast Redis read per request).
- **Capacity test:** CI proves correctness with 10k users on a small runner. The full 100k-in-10s run belongs on production-sized infrastructure, with nodes pre-scaled before 8 PM (autoscaling is too slow for a 10-second spike).
- **Observability:** add Prometheus metrics (reservations, 429/503 rates, queue depth, worker lag) and alerts.
