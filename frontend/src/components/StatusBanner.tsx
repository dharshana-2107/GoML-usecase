import type { Phase } from "../hooks/useCheckout";

interface Props {
  phase: Phase;
  errorText: string;
  offline: boolean;
}

const MESSAGES: Partial<Record<Phase, [tone: string, text: string]>> = {
  reserving: ["info", "Holding a phone for you. Please keep this tab open."],
  queued: ["info", "Your phone is reserved. We're saving your order now."],
  slow: ["info", "Your reservation is safe. Confirmation is taking longer than usual; we'll email you."],
  sold_out: ["warn", "All 500 phones are gone. Thanks for joining the sale!"],
  rejected: ["warn", "Sorry, the last units went just before yours."],
  already_purchased: ["info", "You already have a phone from this sale (limit one per customer)."],
};

// one live region for every status change, so screen readers hear updates
export function StatusBanner({ phase, errorText, offline }: Props) {
  let tone = "";
  let text = "";
  if (phase === "error") [tone, text] = ["error", errorText];
  else if (MESSAGES[phase]) [tone, text] = MESSAGES[phase]!;
  else if (offline) [tone, text] = ["warn", "Reconnecting… your place is not affected."];

  return (
    <p className={`banner ${tone ? `banner-${tone}` : "banner-empty"}`} role="status" aria-live="polite">
      {text}
    </p>
  );
}
