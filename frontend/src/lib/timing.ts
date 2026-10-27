// small timing helpers used across the app.

export const sleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

// throttle runs the function at most once per `waitMs`.
// we use the "leading edge": the first click fires instantly, extra clicks
// inside the window are ignored. this is the right tool for a buy button.
// (debounce would delay the first click until the user stops clicking,
// which is the opposite of what you want when every millisecond counts.)
export function throttle<A extends unknown[]>(fn: (...args: A) => void, waitMs: number) {
  let last = 0;
  return (...args: A) => {
    const now = Date.now();
    if (now - last < waitMs) return;
    last = now;
    fn(...args);
  };
}

// debounce waits until calls stop for `waitMs`, then runs once.
// good for noisy events like tab focus / resize, not for buttons.
export function debounce<A extends unknown[]>(fn: (...args: A) => void, waitMs: number) {
  let timer: ReturnType<typeof setTimeout> | undefined;
  return (...args: A) => {
    clearTimeout(timer);
    timer = setTimeout(() => fn(...args), waitMs);
  };
}

// exponential backoff with "full jitter": spreads retries out randomly so
// thousands of clients don't all retry at the exact same moment.
export function backoff(attempt: number, minMs = 0, baseMs = 300, capMs = 4000) {
  const exp = Math.min(capMs, baseMs * 2 ** attempt);
  return Math.max(minMs, Math.round(exp * (0.5 + Math.random() / 2)));
}

// random id for idempotency keys. falls back when randomUUID is missing
// (it only exists on https or localhost).
export function uuid(): string {
  if (typeof crypto.randomUUID === "function") return crypto.randomUUID();
  const b = crypto.getRandomValues(new Uint8Array(16));
  b[6] = (b[6] & 0x0f) | 0x40;
  b[8] = (b[8] & 0x3f) | 0x80;
  const h = Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}
