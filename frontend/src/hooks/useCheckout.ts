// the buy flow as a small state machine. it guarantees one request in flight,
// shows instant (optimistic) feedback and polls until the order is final.
import { useCallback, useMemo, useRef, useState } from "react";
import { api, ApiError } from "../lib/api";
import { backoff, sleep, throttle, uuid } from "../lib/timing";

export type Phase =
  | "idle" // nothing yet
  | "reserving" // request sent (optimistic: we already show "reserving")
  | "queued" // got a unit, worker is saving the order
  | "confirmed" // saved in the database
  | "slow" // still queued after a long wait
  | "sold_out"
  | "already_purchased"
  | "rejected"
  | "error"; // something failed, user may try again

const BUSY: Phase[] = ["reserving", "queued", "slow"];

export function useCheckout() {
  const [phase, setPhase] = useState<Phase>("idle");
  const [orderId, setOrderId] = useState("");
  const [errorText, setErrorText] = useState("");
  const inFlight = useRef(false);
  const idemKey = useRef(uuid()); // reused across retries of the same attempt
  const token = useRef<{ value: string; expiresAt: number } | null>(null);

  // called a few seconds before the sale: fetch the sale token and
  // pre-load the success screen code, so the click itself is just one request.
  const prepare = useCallback(async () => {
    void import("../components/SuccessPanel"); // asset pre-fetch
    if (token.current && token.current.expiresAt - Date.now() > 60_000) return;
    try {
      const t = await api.token();
      token.current = { value: t.token, expiresAt: t.expiresAt };
    } catch {
      token.current = null; // we'll try again on click
    }
  }, []);

  // poll the order with backoff until the worker gives a final answer
  const waitForOrder = useCallback(async (id: string) => {
    const deadline = Date.now() + 60_000;
    for (let attempt = 0; Date.now() < deadline; attempt++) {
      await sleep(Math.min(3000, backoff(attempt, 400, 400)));
      try {
        const o = await api.order(id);
        if (o.status === "confirmed" || o.status === "rejected") {
          setPhase(o.status);
          return;
        }
      } catch {
        // network blip: keep polling, the order is safe in the queue
      }
    }
    setPhase("slow");
  }, []);

  const buyNow = useCallback(async () => {
    if (inFlight.current) return; // hard guard against double submits
    inFlight.current = true;
    setPhase("reserving"); // optimistic update: instant feedback
    setErrorText("");
    try {
      if (!token.current) await prepare();
      if (!token.current) throw new ApiError(0, "network_error");
      const res = await api.checkout(token.current.value, idemKey.current);
      setOrderId(res.reservationId);
      setPhase("queued");
      await waitForOrder(res.reservationId);
    } catch (e) {
      const code = e instanceof ApiError ? e.code : "network_error";
      if (code === "sold_out" || code === "already_purchased") {
        setPhase(code);
      } else {
        if (code === "invalid_sale_token") token.current = null;
        idemKey.current = uuid(); // a new attempt gets a new key
        setErrorText(messageFor(code));
        setPhase("error"); // roll back the optimistic state
      }
    } finally {
      inFlight.current = false;
    }
  }, [prepare, waitForOrder]);

  // throttle (leading edge): first tap fires instantly, rapid taps are dropped
  const buy = useMemo(() => throttle(() => void buyNow(), 1500), [buyNow]);

  // the stock we show drops by one the moment you tap, before the server answers
  const optimisticTaken = BUSY.includes(phase) ? 1 : 0;

  return { phase, orderId, errorText, buy, prepare, busy: BUSY.includes(phase), optimisticTaken };
}

function messageFor(code: string): string {
  switch (code) {
    case "sale_not_started":
      return "The sale hasn't started yet. Hang tight!";
    case "invalid_sale_token":
      return "Your session expired. Tap again to retry.";
    case "busy":
    case "try_again":
    case "http_429":
      return "Huge demand right now. Tap again in a moment.";
    default:
      return "Connection problem. Tap again to retry.";
  }
}
