// keeps the sale status fresh with "adaptive polling".
//
// why polling and not websockets? 100k open sockets need a lot of server
// memory and sticky load balancing, while the status only changes a few
// times (not started -> live -> sold out). with polling, nginx caches the
// answer for 1 second, so 100k browsers cost the api about 1 request/second.
import { useCallback, useEffect, useRef, useState } from "react";
import { api, SaleStatus } from "../lib/api";
import { debounce } from "../lib/timing";

// poll slowly when nothing is happening, fast right before and during the sale
function nextDelay(s: SaleStatus | null, now: number, failures: number): number | null {
  if (failures > 0) return Math.min(15000, 1000 * 2 ** failures); // back off on errors
  if (!s) return 2000;
  if (s.soldOut) return null; // final state: stop polling
  const untilStart = s.startsAt - now;
  if (untilStart > 60_000) return 10_000;
  if (untilStart > 10_000) return 3000;
  if (untilStart > 0) return 1000;
  return 2000; // live: stock count updates
}

export function useSaleStatus(now: () => number) {
  const [status, setStatus] = useState<SaleStatus | null>(null);
  const [offline, setOffline] = useState(false);
  const nowRef = useRef(now);
  nowRef.current = now;

  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    let failures = 0;
    let stopped = false;
    let latest: SaleStatus | null = null;

    // settimeout chain instead of setinterval: a slow request can never
    // overlap with the next one.
    const tick = async () => {
      clearTimeout(timer);
      if (stopped || document.hidden) return; // no polling in background tabs
      try {
        latest = await api.status();
        failures = 0;
        setStatus(latest);
        setOffline(false);
      } catch {
        failures++;
        setOffline(true);
      }
      const delay = nextDelay(latest, nowRef.current(), failures);
      if (delay !== null && !stopped) timer = setTimeout(tick, delay);
    };

    // when the tab comes back into view, refresh once (debounced, because
    // focus + visibility events often fire together)
    const onVisible = debounce(() => {
      if (!document.hidden) void tick();
    }, 300);

    void tick();
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      stopped = true;
      clearTimeout(timer);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, []);

  return { status, offline };
}

// syncs to the server clock once, so the countdown hits zero at the real
// 8:00:00 pm even if the user's device clock is a few minutes off.
export function useServerClock() {
  const [offset, setOffset] = useState(0);
  useEffect(() => {
    const sentAt = Date.now();
    api
      .time()
      .then(({ serverTime }) => {
        const receivedAt = Date.now();
        // assume the server answered halfway through the round trip
        setOffset(serverTime - (sentAt + receivedAt) / 2);
      })
      .catch(() => setOffset(0)); // fall back to the device clock
  }, []);
  // stable function identity, so effects that depend on it do not restart
  return useCallback(() => Date.now() + offset, [offset]);
}
