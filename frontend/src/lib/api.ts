// thin fetch wrapper: timeouts, safe retries and typed errors.
// who you are is carried by an httponly session cookie that the browser sends
// automatically. javascript never sees it, so it can't be stolen by a script.
import { backoff, sleep } from "./timing";

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    public retryAfterMs = 0,
  ) {
    super(code);
  }
}

const TIMEOUT_MS = 5000;
// only these are worth retrying. 409 (sold out) or 403 will not change.
const RETRYABLE = new Set([429, 502, 503, 504]);

interface Options {
  method?: "GET" | "POST";
  headers?: Record<string, string>;
  body?: unknown;
  retries?: number;
}

export async function request<T>(path: string, opts: Options = {}): Promise<T> {
  const { method = "GET", headers = {}, body, retries = 0 } = opts;
  for (let attempt = 0; ; attempt++) {
    // abort slow requests so the ui never hangs on a dead connection
    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), TIMEOUT_MS);
    let err: ApiError;
    try {
      const res = await fetch(path, {
        method,
        credentials: "same-origin", // send the session cookie
        headers: body === undefined ? headers : { "Content-Type": "application/json", ...headers },
        body: body === undefined ? undefined : JSON.stringify(body),
        signal: ctrl.signal,
      });
      if (res.status === 204) return undefined as T;
      if (res.ok) return (await res.json()) as T;
      const data = await res.json().catch(() => ({}));
      const retryAfter = Number(res.headers.get("Retry-After") ?? 0) * 1000;
      err = new ApiError(res.status, data.error ?? `http_${res.status}`, retryAfter);
      if (!RETRYABLE.has(res.status)) throw err;
    } catch (e) {
      if (e instanceof ApiError && !RETRYABLE.has(e.status)) throw e;
      err = e instanceof ApiError ? e : new ApiError(0, "network_error");
    } finally {
      clearTimeout(timer);
    }
    if (attempt >= retries) throw err;
    await sleep(backoff(attempt, err.retryAfterMs));
  }
}

// --- endpoints ---------------------------------------------------------------
export interface SaleStatus {
  sku: string;
  serverTime: number;
  startsAt: number;
  live: boolean;
  remaining: number;
  soldOut: boolean;
}
export interface SaleToken {
  token: string;
  expiresAt: number;
}
export interface Reservation {
  reservationId: string;
  status: "pending" | "confirmed" | "rejected";
}
export interface User {
  userId: string;
  email?: string;
}

export const api = {
  // auth: login/register are not retried, so a wrong password isn't sent twice
  me: () => request<User>("/api/auth/me", { retries: 2 }),
  login: (email: string, password: string) =>
    request<User>("/api/auth/login", { method: "POST", body: { email, password } }),
  register: (email: string, password: string) =>
    request<User>("/api/auth/register", { method: "POST", body: { email, password } }),
  logout: () => request<void>("/api/auth/logout", { method: "POST" }),

  // sale
  time: () => request<{ serverTime: number }>("/api/time"),
  status: () => request<SaleStatus>("/api/sale/status"),
  token: () => request<SaleToken>("/api/sale/token", { method: "POST", retries: 3 }),
  // the same idempotency key is sent on every retry, so a retry can never
  // buy a second phone even if the first request actually succeeded.
  checkout: (token: string, idempotencyKey: string) =>
    request<Reservation>("/api/checkout", {
      method: "POST",
      headers: { "X-Sale-Token": token, "Idempotency-Key": idempotencyKey },
      retries: 3,
    }),
  order: (id: string) => request<Reservation>(`/api/orders/${encodeURIComponent(id)}`, { retries: 2 }),
};
