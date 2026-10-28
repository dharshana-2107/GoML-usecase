import type { Phase } from "../hooks/useCheckout";

interface Props {
  phase: Phase;
  errorText: string;
  offline: boolean;
}

const MESSAGES: Partial<Record<Phase, [tone: string, text: string]>> = {
  reserving: ["info", "Holding a phone for you. Keep this tab open."],
  queued: ["info", "Your phone is reserved. Confirming the order now."],
  slow: ["info", "Your phone is reserved. Confirmation is taking longer than usual, and we'll email you when it's done."],
  sold_out: ["warn", "All 500 phones are sold. Thanks for coming."],
  rejected: ["warn", "The last phone went just before yours."],
  already_purchased: ["info", "You already have one from this sale. It's one per customer."],
};

// one live region for every status change, so screen readers hear updates
export function StatusBanner({ phase, errorText, offline }: Props) {
  let tone = "";
  let text = "";
  if (phase === "error") [tone, text] = ["error", errorText];
  else if (MESSAGES[phase]) [tone, text] = MESSAGES[phase]!;
  else if (offline) [tone, text] = ["warn", "Reconnecting. Your place isn't affected."];

  return (
    <p className={`status ${tone ? `status-${tone}` : ""}`} role="status" aria-live="polite">
      {text}
    </p>
  );
}
