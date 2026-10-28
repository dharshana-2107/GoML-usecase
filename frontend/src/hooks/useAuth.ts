// who is signed in. on page load we ask the server (/api/auth/me), which
// only checks the cookie signature, so it's cheap even with 100k visitors.
import { useCallback, useEffect, useState } from "react";
import { api } from "../lib/api";

export type AuthState =
  | { status: "loading" }
  | { status: "signed_out" }
  | { status: "signed_in"; userId: string; email: string };

// the email is only kept for display. the server never trusts it.
const EMAIL_KEY = "fs-email";
function rememberEmail(email: string) {
  try {
    localStorage.setItem(EMAIL_KEY, email);
  } catch {
    // storage blocked (private mode): just don't show the email
  }
}
function savedEmail(): string {
  try {
    return localStorage.getItem(EMAIL_KEY) ?? "";
  } catch {
    return "";
  }
}

export function useAuth() {
  const [state, setState] = useState<AuthState>({ status: "loading" });

  useEffect(() => {
    api
      .me()
      .then((u) => setState({ status: "signed_in", userId: u.userId, email: savedEmail() }))
      // 401 = not signed in. any other error also shows the login form,
      // which is safe: signing in again just replaces the cookie.
      .catch(() => setState({ status: "signed_out" }));
  }, []);

  const signIn = useCallback(async (mode: "login" | "register", email: string, password: string) => {
    const u = mode === "login" ? await api.login(email, password) : await api.register(email, password);
    rememberEmail(u.email ?? email);
    setState({ status: "signed_in", userId: u.userId, email: u.email ?? email });
  }, []);

  const signOut = useCallback(async () => {
    await api.logout().catch(() => undefined); // clear the ui even if the call fails
    setState({ status: "signed_out" });
  }, []);

  return { state, signIn, signOut };
}
