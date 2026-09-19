import { useState, useEffect } from "react";
import {
  UserView,
  createUser,
  deleteUser,
  listUsers,
} from "../utils/api";

interface UserManagementProps {
  apiBaseUrl: string;
}

/**
 * Admin-only user management panel. Lists all users, creates new ones
 * (with optional admin flag) and deletes users. Mirrors:
 *   GET    /api/auth/users
 *   POST   /api/auth/users
 *   DELETE /api/auth/users/{id}
 */
export const UserManagement: React.FC<UserManagementProps> = ({ apiBaseUrl }) => {
  const [users, setUsers] = useState<UserView[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Form fields
  const [loginName, setLoginName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [isAdmin, setIsAdmin] = useState(false);

  const loadUsers = async () => {
    try {
      const data = await listUsers(apiBaseUrl);
      setUsers(data);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load users");
    }
  };

  useEffect(() => {
    loadUsers();
  }, [apiBaseUrl]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    if (!loginName || !password || !email) {
      setError("All fields are required.");
      return;
    }
    setBusy(true);
    try {
      await createUser(apiBaseUrl, {
        login_name: loginName,
        email,
        password,
        is_admin: isAdmin,
      });
      // Reset form and reload
      setLoginName("");
      setEmail("");
      setPassword("");
      setIsAdmin(false);
      await loadUsers();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to create user");
    } finally {
      setBusy(false);
    }
  };

  const handleDelete = async (user: UserView) => {
    if (!confirm(`Delete user "${user.login_name}"? This cannot be undone.`)) {
      return;
    }
    setError(null);
    setBusy(true);
    try {
      await deleteUser(apiBaseUrl, user.id);
      await loadUsers();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to delete user");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="user-panel-group">
      <div className="user-panel-section-title">
        <span className="user-panel-icon">🛡️</span>
        User Management
      </div>

      {/* Create user form */}
      <form className="user-form" onSubmit={handleSubmit}>
        <div className="user-form-title">Create user</div>
        <label>
          <span className="user-form-label">Username</span>
          <input
            type="text"
            value={loginName}
            onChange={(e) => setLoginName(e.target.value)}
            placeholder="new-user"
            autoComplete="username"
            disabled={busy}
          />
        </label>
        <label>
          <span className="user-form-label">Email</span>
          <input
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            placeholder="user@example.com"
            autoComplete="email"
            disabled={busy}
          />
        </label>
        <label>
          <span className="user-form-label">Password</span>
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder="••••••••"
            autoComplete="new-password"
            disabled={busy}
          />
        </label>
        <label className="user-form-checkbox">
          <input
            type="checkbox"
            checked={isAdmin}
            onChange={(e) => setIsAdmin(e.target.checked)}
            disabled={busy}
          />
          <span>Admin</span>
        </label>
        {error && <div className="user-form-error">{error}</div>}
        <button type="submit" className="user-form-submit" disabled={busy}>
          {busy ? "Creating…" : "Create User"}
        </button>
      </form>

      {/* User list */}
      <div className="user-list">
        {users.map((user) => (
          <div className="user-list-item" key={user.id}>
            <div className="user-list-item-info">
              <span className="user-list-item-name">{user.login_name}</span>
              <span className="user-list-item-email">{user.email}</span>
              {user.is_admin && (
                <span className="user-list-item-badge">admin</span>
              )}
            </div>
            {user.id !== 1 && (
              <button
                className="user-list-item-delete"
                onClick={() => handleDelete(user)}
                disabled={busy}
                title="Delete user"
              >
                🗑️
              </button>
            )}
          </div>
        ))}
      </div>
    </div>
  );
};
