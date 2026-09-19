import React, { useState } from "react";
import { LoginRequest } from "../hooks/useAuth";

interface LoginProps {
  login: (req: LoginRequest) => Promise<void>;
}

/**
 * Login screen. Self-registration has been disabled server-side (POST
 * /api/auth/register now returns 403); users are created by an admin via
 * the User Management panel. So this form only supports login.
 */
export const Login: React.FC<LoginProps> = ({ login }) => {
  const [loginName, setLoginName] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      const req: LoginRequest = { username: loginName, password };
      await login(req);
    } catch (err) {
      const msg = err instanceof Error ? err.message : "Login failed";
      // Normalise backend "invalid credentials" -> clearer message
      const normalised = /invalid/i.test(msg)
        ? "Invalid username or password"
        : msg;
      setError(normalised);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="login-container">
      <div className="login-card">
        <div className="login-logo">⚡<span>SupersonicIQ</span></div>
        <p className="login-sub">Sign in to continue</p>

        <form className="login-form" onSubmit={submit}>
          <label>
            <span className="login-label">Username</span>
            <input
              type="text"
              value={loginName}
              onChange={(e) => setLoginName(e.target.value)}
              placeholder="your-username"
              autoComplete="username"
              autoFocus
              disabled={busy}
            />
          </label>

          <label>
            <span className="login-label">Password</span>
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder="••••••••"
              autoComplete="current-password"
              disabled={busy}
            />
          </label>

          {error && <div className="login-error">{error}</div>}

          <button type="submit" className="login-submit" disabled={busy}>
            {busy ? "Please wait…" : "Sign In"}
          </button>
        </form>

        <p className="login-hint">
          Registration is disabled. Contact an administrator if you need an account.
        </p>
      </div>
    </div>
  );
};
