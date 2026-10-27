import type { Phase } from "../hooks/useCheckout";

interface Props {
  phase: Phase;
  live: boolean;
  busy: boolean;
  onBuy: () => void;
}

const LABELS: Partial<Record<Phase, string>> = {
  reserving: "Reserving your phone…",
  queued: "You're in! Confirming…",
  slow: "Still confirming…",
  confirmed: "Order confirmed",
  sold_out: "Sold out",
  already_purchased: "Already purchased",
  rejected: "Sold out",
  error: "Try again",
};

// the button is disabled while a request is in flight and after a final
// answer, so the ui itself can not spam the api.
export function BuyButton({ phase, live, busy, onBuy }: Props) {
  const done = ["confirmed", "sold_out", "already_purchased", "rejected"].includes(phase);
  const label = LABELS[phase] ?? (live ? "Buy now" : "Opens at 8:00 PM");

  return (
    <button
      type="button"
      className={`buy ${busy ? "is-busy" : ""}`}
      disabled={!live || busy || done}
      aria-busy={busy}
      onClick={onBuy}
    >
      {busy && <span className="spinner" aria-hidden="true" />}
      {label}
    </button>
  );
}
