// the banner across the top. big with a countdown before the sale, then a
// slim strip once the sale is live or sold out.
import type { SaleStatus } from "../lib/api";
import { Countdown } from "./Countdown";
import { formatTime, PRICE_TEXT, PRODUCT, TOTAL_UNITS } from "./Product";

interface Props {
  status: SaleStatus | null;
  now: () => number;
  remaining: number;
  onStart: () => void;
}

export function SaleBanner({ status, now, remaining, onStart }: Props) {
  if (!status) return <div className="banner banner-slim" aria-hidden="true" />;

  const started = now() >= status.startsAt;

  if (!started) {
    return (
      <section className="banner" aria-labelledby="banner-title">
        <div className="banner-copy">
          <p className="pill">Festival flash sale</p>
          <h1 id="banner-title">
            {PRODUCT} for {PRICE_TEXT}
          </h1>
          <p>Only {TOTAL_UNITS} phones, one per customer. Sale starts at {formatTime(status.startsAt)}.</p>
        </div>
        <Countdown startsAt={status.startsAt} now={now} onDone={onStart} />
      </section>
    );
  }

  return (
    <section className="banner banner-slim" aria-live="polite">
      {status.soldOut || remaining === 0 ? (
        <p>
          <strong>Sold out.</strong> All {TOTAL_UNITS} phones have found owners. Thanks for shopping.
        </p>
      ) : (
        <p>
          <strong>The sale is live.</strong> {remaining} of {TOTAL_UNITS} phones left.
        </p>
      )}
    </section>
  );
}
