// the store header: logo on the left, account on the right
interface Props {
  email: string;
  signedIn: boolean;
  busy: boolean;
  onSignOut: () => void;
}

export function Masthead({ email, signedIn, busy, onSignOut }: Props) {
  return (
    <header className="masthead">
      <div className="masthead-inner">
        <span className="logo">
          <span className="logo-mark" aria-hidden="true" />
          Nova Store
        </span>
        {signedIn && (
          <div className="account">
            <span className="who">{email || "Signed in"}</span>
            <button type="button" className="link" onClick={onSignOut} disabled={busy}>
              Sign out
            </button>
          </div>
        )}
      </div>
    </header>
  );
}
