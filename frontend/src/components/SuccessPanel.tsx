// loaded lazily: most visitors never reach this screen, so it is not part of
// the first download. it is pre-fetched a few seconds before the sale opens.
export default function SuccessPanel({ orderId }: { orderId: string }) {
  return (
    <section className="success" aria-labelledby="success-title">
      <svg width="48" height="48" viewBox="0 0 48 48" aria-hidden="true">
        <circle cx="24" cy="24" r="22" fill="currentColor" opacity="0.15" />
        <path d="M14 25l7 7 13-15" fill="none" stroke="currentColor" strokeWidth="4" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
      <h2 id="success-title">It's yours!</h2>
      <p>Your order is confirmed. We'll send payment and delivery details to your email.</p>
      <p className="muted">
        Order reference <code>{orderId.slice(0, 8).toUpperCase()}</code>
      </p>
    </section>
  );
}
