// loaded lazily: most visitors never reach this screen, so it is not part of
// the first download. it is pre-fetched a few seconds before the sale opens.
export default function SuccessPanel({ orderId }: { orderId: string }) {
  return (
    <section className="success" aria-labelledby="success-title">
      <h2 id="success-title">It's yours.</h2>
      <p>Your X Phone Ultra is confirmed. We'll email payment and delivery details.</p>
      <p className="success-ref">
        Order <strong>{orderId.slice(0, 8).toUpperCase()}</strong>
      </p>
    </section>
  );
}
