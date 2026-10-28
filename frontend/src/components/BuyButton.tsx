import type { Phase } from "../hooks/useCheckout";

interface Props {
  phase: Phase;
  live: boolean;
  soldOut: boolean;
  busy: boolean;
  opensAt: string; // like "8:00 PM", from the server's start time
  onBuy: () => void;
}

const LABELS: Partial<Record<Phase, string>> = {
  reserving: "Reserving…",
  queued: "Confirming…",
  slow: "Still confirming…",
  sold_out: "Sold out",
  rejected: "Sold out",
  already_purchased: "Already bought",
  error: "Book now",
};

// the button is disabled while a request is in flight and after a final
// answer, so the ui itself can not spam the api.
export function BuyButton({ phase, live, soldOut, busy, opensAt, onBuy }: Props) {
  const done = ["confirmed", "sold_out", "already_purchased", "rejected"].includes(phase);
  const idle = soldOut ? "Sold out" : live ? "Book now" : `Opens at ${opensAt}`;
  const label = LABELS[phase] ?? idle;

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
