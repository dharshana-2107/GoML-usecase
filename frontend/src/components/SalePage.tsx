// the sale page itself, shown once the user is signed in.
import { lazy, Suspense, useCallback, useEffect, useState } from "react";
import { BuyButton } from "./BuyButton";
import { Countdown } from "./Countdown";
import { StatusBanner } from "./StatusBanner";
import { useCheckout } from "../hooks/useCheckout";
import { useSaleStatus, useServerClock } from "../hooks/useSaleStatus";

const SuccessPanel = lazy(() => import("./SuccessPanel"));
const TOTAL_UNITS = 500;

interface Props {
  email: string;
  onSignOut: () => void;
  onSessionExpired: () => void;
}

export function SalePage({ email, onSignOut, onSessionExpired }: Props) {
  const now = useServerClock();
  const { status, offline } = useSaleStatus(now);
  const checkout = useCheckout(onSessionExpired);
  const { prepare } = checkout;

  // already close to (or past) the start when the page opens? prepare now.
  useEffect(() => {
    if (status && status.startsAt - now() < 15_000) void prepare();
  }, [status, now, prepare]);
  const onFinalSeconds = useCallback(() => void prepare(), [prepare]);
  // re-render the moment the countdown hits zero so the button unlocks instantly
  const [, setTick] = useState(0);
  const onDone = useCallback(() => setTick((n) => n + 1), []);

  const started = !!status && now() >= status.startsAt;
  const live = started && !status.soldOut;
  // optimistic stock: subtract our own pending reservation right away
  const remaining = status ? Math.max(0, status.remaining - checkout.optimisticTaken) : TOTAL_UNITS;

  return (
    <main className="shell">
      <header>
        <p className="eyebrow">Festival Flash Sale · Tonight 8:00 PM</p>
        <h1>Nova X1 Smartphone</h1>
        <p className="muted">Only {TOTAL_UNITS} units. One per customer.</p>
        <p className="account">
          Signed in{email ? ` as ${email}` : ""} ·{" "}
          <button type="button" className="link" onClick={onSignOut} disabled={checkout.busy}>
            Sign out
          </button>
        </p>
      </header>

      <section className="card">
        <PhoneArt />
        <div className="price">
          <span className="now">₹24,999</span> <s className="muted">₹39,999</s>
        </div>

        {status && !started && <Countdown startsAt={status.startsAt} now={now} onFinalSeconds={onFinalSeconds} onDone={onDone} />}

        {started && (
          <div className="meter" aria-label={`${remaining} of ${TOTAL_UNITS} left`}>
            <div className="meter-fill" style={{ width: `${(remaining / TOTAL_UNITS) * 100}%` }} />
            <span>{remaining > 0 ? `${remaining} left` : "Sold out"}</span>
          </div>
        )}

        {checkout.phase === "confirmed" ? (
          <Suspense fallback={<p className="muted">Order confirmed!</p>}>
            <SuccessPanel orderId={checkout.orderId} />
          </Suspense>
        ) : (
          <BuyButton phase={checkout.phase} live={live} busy={checkout.busy} onBuy={checkout.buy} />
        )}

        <StatusBanner phase={checkout.phase} errorText={checkout.errorText} offline={offline} />
      </section>
    </main>
  );
}

// inline svg instead of a product photo: zero extra requests, tiny bundle
function PhoneArt() {
  return (
    <svg className="phone" viewBox="0 0 120 200" role="img" aria-label="Nova X1 phone">
      <rect x="10" y="4" width="100" height="192" rx="18" fill="#0f172a" />
      <rect x="16" y="14" width="88" height="172" rx="12" fill="url(#screen)" />
      <rect x="48" y="18" width="24" height="6" rx="3" fill="#0f172a" />
      <defs>
        <linearGradient id="screen" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" stopColor="#f97316" />
          <stop offset="1" stopColor="#7c3aed" />
        </linearGradient>
      </defs>
    </svg>
  );
}
