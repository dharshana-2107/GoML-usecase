// the whole store page: header, sale banner, then the product with the phone
// on the left and price + sign in / book now on the right.
import { useCallback, useEffect, useState } from "react";
import { LoginForm } from "./components/LoginForm";
import { Masthead } from "./components/Masthead";
import { BuyBox, formatTime, ProductShowcase, TOTAL_UNITS } from "./components/Product";
import { Purchase } from "./components/Purchase";
import { SaleBanner } from "./components/SaleBanner";
import { useAuth } from "./hooks/useAuth";
import { useCheckout } from "./hooks/useCheckout";
import { useSaleStatus, useServerClock } from "./hooks/useSaleStatus";

export default function App() {
  const { state, signIn, signOut } = useAuth();
  const now = useServerClock();
  const { status, offline } = useSaleStatus(now); // public, works signed out too
  // the login expired mid-sale: drop back to the sign-in form
  const onSessionExpired = useCallback(() => void signOut(), [signOut]);
  const checkout = useCheckout(onSessionExpired);
  const { prepare } = checkout;
  const signedIn = state.status === "signed_in";

  // re-render the moment the countdown hits zero so the button unlocks instantly
  const [, setTick] = useState(0);
  const onStart = useCallback(() => setTick((n) => n + 1), []);

  // 15 seconds before the start: fetch the sale token and pre-load the success
  // screen, so tapping "book now" is a single request
  useEffect(() => {
    if (!signedIn || !status) return;
    const wait = status.startsAt - 15_000 - now();
    if (wait <= 0) {
      void prepare();
      return;
    }
    const t = setTimeout(() => void prepare(), wait);
    return () => clearTimeout(t);
  }, [signedIn, status, now, prepare]);

  const started = !!status && now() >= status.startsAt;
  const live = started && !status.soldOut;
  // optimistic stock: subtract our own pending reservation right away
  const remaining = status ? Math.max(0, status.remaining - checkout.optimisticTaken) : TOTAL_UNITS;
  const opensAt = status ? formatTime(status.startsAt) : "…";

  return (
    <>
      <Masthead
        email={signedIn ? state.email : ""}
        signedIn={signedIn}
        onSignOut={() => void signOut()}
        busy={checkout.busy}
      />
      <main className="page">
        <SaleBanner status={status} now={now} remaining={remaining} onStart={onStart} />

        <section className="product" aria-label="X Phone Ultra">
          <ProductShowcase />
          <BuyBox remaining={remaining}>
            {state.status === "loading" ? (
              <p className="status">Loading…</p>
            ) : signedIn ? (
              <Purchase
                phase={checkout.phase}
                orderId={checkout.orderId}
                errorText={checkout.errorText}
                busy={checkout.busy}
                live={live}
                soldOut={!!status?.soldOut}
                offline={offline}
                opensAt={opensAt}
                onBook={checkout.buy}
              />
            ) : (
              <LoginForm onSubmit={signIn} />
            )}
          </BuyBox>
        </section>
      </main>
    </>
  );
}
