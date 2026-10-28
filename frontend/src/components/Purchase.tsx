// the action area for a signed-in shopper: book button, status, or success.
import { lazy, Suspense } from "react";
import { BuyButton } from "./BuyButton";
import { StatusBanner } from "./StatusBanner";
import type { Phase } from "../hooks/useCheckout";

const SuccessPanel = lazy(() => import("./SuccessPanel"));

interface Props {
  phase: Phase;
  orderId: string;
  errorText: string;
  busy: boolean;
  live: boolean;
  soldOut: boolean;
  offline: boolean;
  opensAt: string;
  onBook: () => void;
}

export function Purchase(p: Props) {
  if (p.phase === "confirmed") {
    return (
      <Suspense fallback={<p className="status">Order confirmed.</p>}>
        <SuccessPanel orderId={p.orderId} />
      </Suspense>
    );
  }
  return (
    <div className="purchase">
      <BuyButton phase={p.phase} live={p.live} soldOut={p.soldOut} busy={p.busy} opensAt={p.opensAt} onBuy={p.onBook} />
      <StatusBanner phase={p.phase} errorText={p.errorText} offline={p.offline} />
    </div>
  );
}
