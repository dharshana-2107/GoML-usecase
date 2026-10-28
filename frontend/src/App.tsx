// the app decides what to show: a loading line, the sign-in form, or the sale.
import { useCallback } from "react";
import { LoginForm } from "./components/LoginForm";
import { SalePage } from "./components/SalePage";
import { useAuth } from "./hooks/useAuth";

export default function App() {
  const { state, signIn, signOut } = useAuth();
  // the login expired mid-sale: drop back to the sign-in form
  const onSessionExpired = useCallback(() => void signOut(), [signOut]);

  if (state.status === "loading") {
    return (
      <main className="shell">
        <p className="muted">Loading sale…</p>
      </main>
    );
  }

  if (state.status === "signed_out") {
    return (
      <main className="shell">
        <header>
          <p className="eyebrow">Festival Flash Sale · Tonight 8:00 PM</p>
          <h1>Nova X1 Smartphone</h1>
          <p className="muted">500 units, one per customer. Sign in before the sale starts.</p>
        </header>
        <LoginForm onSubmit={signIn} />
      </main>
    );
  }

  return <SalePage email={state.email} onSignOut={() => void signOut()} onSessionExpired={onSessionExpired} />;
}
