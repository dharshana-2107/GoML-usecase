// thin fetch wrapper: timeouts, safe retries and typed errors.
import { backoff, sleep, uuid } from "./timing";

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
  retries?: number;
}

export async function request<T>(path: string, opts: Options = {}): Promise<T> {
  const { method = "GET", headers = {}, retries = 0 } = opts;
  for (let attempt = 0; ; attempt++) {
    // abort slow requests so the ui never hangs on a dead connection
    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), TIMEOUT_MS);
    let err: ApiError;
    try {
      const res = await fetch(path, {
        method,
        headers: { "X-User-ID": userId(), ...headers },
        signal: ctrl.signal,
      });
      if (res.ok) return (await res.json()) as T;
      const body = await res.json().catch(() => ({}));
      const retryAfter = Number(res.headers.get("Retry-After") ?? 0) * 1000;
      err = new ApiError(res.status, body.error ?? `http_${res.status}`, retryAfter);
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

// --- demo identity ---------------------------------------------------------
// a real app reads the user from its login session cookie on the server.
// here we keep a random id in localStorage so you can test in many tabs
// (use a private window for a "new" user).
let cachedUser = "";
export function userId(): string {
  if (cachedUser) return cachedUser;
  try {
    cachedUser = localStorage.getItem("demo-user-id") ?? "";
    if (!cachedUser) {
      cachedUser = `user_${uuid().slice(0, 12)}`;
      localStorage.setItem("demo-user-id", cachedUser);
    }
  } catch {
    cachedUser = `user_${uuid().slice(0, 12)}`; // storage blocked: per-tab id
  }
  return cachedUser;
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

export const api = {
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
