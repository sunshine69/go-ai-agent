import { useState } from "react";
import { changePassword } from "../utils/api";

interface ChangePasswordProps {
  apiBaseUrl: string;
}

/**
 * A compact "Change password" form available to every logged-in user.
 * Mirrors POST /api/auth/me/profile/password on the backend.
 */
export const ChangePassword: React.FC<ChangePasswordProps> = ({ apiBaseUrl }) => {
  const [oldPassword, setOldPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [ok, setOk] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    setOk(false);
    if (newPassword !== confirmPassword) {
      setError("New passwords do not match.");
      return;
    }
    if (newPassword.length < 6) {
      setError("New password must be at least 6 characters.");
      return;
    }
    setBusy(true);
    try {
      await changePassword(apiBaseUrl, { old_password: oldPassword, new_password: newPassword });
      setOk(true);
      setOldPassword("");
      setNewPassword("");
      setConfirmPassword("");
    } catch (err) {
      const msg = err instanceof Error ? err.message : "Failed to change password";
      setError(msg);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="user-panel-group">
      <div className="user-panel-section-title">Change Password</div>
      <form className="user-form" onSubmit={submit}>
        <label>
          <span className="user-form-label">Current password</span>
          <input
            type="password"
            value={oldPassword}
            onChange={(e) => setOldPassword(e.target.value)}
            placeholder="••••••••"
            autoComplete="current-password"
            disabled={busy}
          />
        </label>

        <label>
          <span className="user-form-label">New password</span>
          <input
            type="password"
            value={newPassword}
            onChange={(e) => setNewPassword(e.target.value)}
            placeholder="••••••••"
            autoComplete="new-password"
            disabled={busy}
          />
        </label>

        <label>
          <span className="user-form-label">Confirm new password</span>
          <input
            type="password"
            value={confirmPassword}
            onChange={(e) => setConfirmPassword(e.target.value)}
            placeholder="••••••••"
            autoComplete="new-password"
            disabled={busy}
          />
        </label>

        {error && <div className="user-form-error">{error}</div>}
        {ok && <div className="user-form-ok">Password updated.</div>}

        <button type="submit" className="user-form-submit" disabled={busy}>
          {busy ? "Updating…" : "Change Password"}
        </button>
      </form>
    </div>
  );
};
