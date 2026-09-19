/**
 * Token persistence for the SPA.
 *
 * The Go backend issues an opaque HS256 JWT access token on login/register
 * (POST /api/auth/login|register -> { access_token, token_type, user }). The
 * browser client stores that token in localStorage and re-sends it on every
 * subsequent request in the "Authorization: Bearer <token>" header (see
 * api.ts). Tokens expire server-side (db.TokenExpiry); an expired/revoked token
 * makes the backend answer 401, at which point this module drops the session.
 */

export interface AuthUser {
  id: number;
  login_name: string;
  email: string;
  is_admin: boolean;
  created_at: string;
  last_login?: string;
}

const TOKEN_KEY = "access_token";
const USER_KEY = "auth_user";

// Safe localStorage helpers — reading/writing can throw in private mode or when
// storage is disabled, so every access is guarded and degrades to a no-op.
export function saveToken(token: string): void {
  try {
    localStorage.setItem(TOKEN_KEY, token);
  } catch {
    /* ignore storage failures */
  }
}

export function getToken(): string | null {
  try {
    return localStorage.getItem(TOKEN_KEY);
  } catch {
    return null;
  }
}

export function clearToken(): void {
  try {
    localStorage.removeItem(TOKEN_KEY);
  } catch {
    /* ignore */
  }
}

export function saveUser(user: AuthUser): void {
  try {
    localStorage.setItem(USER_KEY, JSON.stringify(user));
  } catch {
    /* ignore */
  }
}

export function getUser(): AuthUser | null {
  try {
    const raw = localStorage.getItem(USER_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as AuthUser;
    return parsed && typeof parsed.id === "number" ? parsed : null;
  } catch {
    return null;
  }
}

export function clearUser(): void {
  try {
    localStorage.removeItem(USER_KEY);
  } catch {
    /* ignore */
  }
}

/** Whether a (possibly stale) session token is stored locally. */
export function hasStoredSession(): boolean {
  return getToken() !== null;
}
