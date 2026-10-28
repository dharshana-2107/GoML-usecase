import { FormEvent, useState } from "react";
import { ApiError } from "../lib/api";

interface Props {
  onSubmit: (mode: "login" | "register", email: string, password: string) => Promise<void>;
}

const MESSAGES: Record<string, string> = {
  wrong_email_or_password: "Wrong email or password.",
  email_taken: "That email already has an account. Try signing in.",
  invalid_email: "Please enter a valid email address.",
  weak_password: "Password must be 8 to 72 characters.",
  http_429: "Too many attempts. Please wait a minute and try again.",
};

// sign in or create an account. checks run in the browser first, then the
// server checks everything again (the browser checks are only for speed).
export function LoginForm({ onSubmit }: Props) {
  const [mode, setMode] = useState<"login" | "register">("login");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (busy) return; // no double submits
    if (!/^\S+@\S+\.\S+$/.test(email.trim())) return setError(MESSAGES.invalid_email);
    if (password.length < 8 || password.length > 72) return setError(MESSAGES.weak_password);

    setBusy(true);
    setError("");
    try {
      await onSubmit(mode, email.trim(), password);
    } catch (err) {
      const code = err instanceof ApiError ? err.code : "network_error";
      setError(MESSAGES[code] ?? "Couldn't reach the store. Check your connection and try again.");
      setBusy(false);
    }
  }

  const isLogin = mode === "login";
  return (
    <form className="login" onSubmit={submit} noValidate>
      <h2>{isLogin ? "Sign in to book" : "Create an account to book"}</h2>

      <label>
        Email
        <input
          type="email"
          autoComplete="email"
          value={email}
          onChange={(e) => (setEmail(e.target.value), setError(""))}
          required
        />
      </label>
      <label>
        Password
        <input
          type="password"
          autoComplete={isLogin ? "current-password" : "new-password"}
          value={password}
          onChange={(e) => (setPassword(e.target.value), setError(""))}
          minLength={8}
          maxLength={72}
          required
        />
      </label>

      <p className="status status-error" role="alert">
        {error}
      </p>

      <button type="submit" className={`buy ${busy ? "is-busy" : ""}`} disabled={busy} aria-busy={busy}>
        {busy && <span className="spinner" aria-hidden="true" />}
        {isLogin ? "Sign in" : "Create account"}
      </button>

      <button
        type="button"
        className="link"
        onClick={() => (setMode(isLogin ? "register" : "login"), setError(""))}
      >
        {isLogin ? "New here? Create an account" : "Have an account? Sign in"}
      </button>
    </form>
  );
}
