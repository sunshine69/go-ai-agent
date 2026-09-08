
// --- User management API (see routers/auth.go) ---

// UserView is the client-side shape of a user record, matching the Go
// authUserView returned by GET /api/auth/users and POST /api/auth/users.
export interface UserView {
  id: number;
  login_name: string;
  email: string;
  is_admin: boolean;
  created_at: string;
  last_login?: string;
}

// CreateUserRequest is the body for POST /api/auth/users (admin only).
export interface CreateUserRequest {
  login_name: string;
  email: string;
  password: string;
  is_admin: boolean;
}

// ChangePasswordRequest is the body for POST /api/auth/me/profile/password.
export interface ChangePasswordRequest {
  old_password: string;
  new_password: string;
}

/** Lists all users (admin only) — GET /api/auth/users. */
export async function listUsers(apiBaseUrl: string): Promise<UserView[]> {
  return getJSON<UserView[]>(apiBaseUrl, "/api/auth/users");
}

/** Creates a user (admin only) — POST /api/auth/users. */
export async function createUser(
  apiBaseUrl: string,
  payload: CreateUserRequest
): Promise<UserView> {
  return postJSON<UserView>(apiBaseUrl, "/api/auth/users", payload);
}

/** Deletes a user (admin only) — DELETE /api/auth/users/{id}. */
export async function deleteUser(
  apiBaseUrl: string,
  userId: number
): Promise<void> {
  return deleteJSON(apiBaseUrl, `/api/auth/users/${userId}`);
}

/** Changes the caller's own password — POST /api/auth/me/profile/password. */
export async function changePassword(
  apiBaseUrl: string,
  payload: ChangePasswordRequest
): Promise<void> {
  const res = await fetchWithToken(apiBaseUrl, {
    url: "/api/auth/me/profile/password",
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
  if (!res.ok) {
    throw new Error(await readErrorText(res));
  }
}
/**
 * A fetch wrapper that attaches the bearer token and understands backend
 * auth-rejection semantics.
 *
 * The Go backend requires a valid "Authorization: Bearer <jwt>" on the API
 * endpoints the SPA calls (see routers.requireAuth in the mux). This wrapper:
 *   1. injects the stored token into every request, and
 *   2. surfaces a 401/403 response as a typed AuthError so callers can react
 *      (e.g. drop the session and return to the login screen) instead of
 *      silently swallowing it.
 *
 * Because the SPA is served same-origin from backend-go under /frontend/*,
 * apiBaseUrl is normally empty (relative requests to /api/*).
 */

import { getToken } from "./token";

export class AuthError extends Error {
  constructor(status: number, message: string) {
    super(message);
    this.name = "AuthError";
    this.status = status;
  }
  public readonly status: number;
}

// Extract the JSON "detail" string from a backend error body when present.
async function readErrorText(res: Response): Promise<string> {
  const text = await res.text();
  try {
    const parsed = JSON.parse(text);
    if (typeof parsed?.detail === "string") return parsed.detail;
    if (typeof parsed?.message === "string") return parsed.message;
  } catch {
    /* not JSON */
  }
  return text || `HTTP ${res.status}`;
}

/**
 * fetchWithToken behaves like fetch but always attaches the bearer token and
 * throws AuthError on 401/403 so the caller can handle session expiry.
 */
export function fetchWithToken(apiBaseUrl: string, input: RequestInit & { url: string }): Promise<Response> {
  const headers: Record<string, string> = { ...(input.headers as Record<string, string>) };
  const token = getToken();
  if (token) headers["Authorization"] = `Bearer ${token}`;

  const res = fetch(`${apiBaseUrl}${input.url}`, {
    ...input,
    headers,
  });
  return res.then(async (r) => {
    if (r.status === 401 || r.status === 403) {
      // Attempt to consume the body; ignore if already read.
      void readErrorText(r).then(() => undefined).catch(() => undefined);
      throw new AuthError(r.status, `Unauthorized (HTTP ${r.status})`);
    }
    return r;
  });
}

/** Minimal JSON POST helper using fetchWithToken. */
export async function postJSON<T>(
  apiBaseUrl: string,
  url: string,
  body: unknown
): Promise<T> {
  const res = await fetchWithToken(apiBaseUrl, {
    url,
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    throw new Error(await readErrorText(res));
  }
  return (await res.json()) as T;
}

/** Minimal JSON GET helper using fetchWithToken. */
export async function getJSON<T>(
  apiBaseUrl: string,
  url: string
): Promise<T> {
  const res = await fetchWithToken(apiBaseUrl, { url, method: "GET" });
  if (!res.ok) {
    throw new Error(await readErrorText(res));
  }
  return (await res.json()) as T;
}

/**
 * Bulk-deletes conversations (multi-select) — POST /api/conversations/bulk-delete.
 * Returns the number of conversations actually deleted. Non-owned or malformed
 * ids are silently ignored by the backend rather than treated as an error.
 */
export async function deleteConversations(
  apiBaseUrl: string,
  ids: string[]
): Promise<number> {
  const res = await fetchWithToken(apiBaseUrl, {
    url: "/api/conversations/bulk-delete",
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ ids }),
  });
  if (!res.ok) {
    throw new Error(await readErrorText(res));
  }
  const data = (await res.json()) as { deleted?: number };
  return data.deleted ?? 0;
}

/** Minimal DELETE helper using fetchWithToken. */
export async function deleteJSON(
  apiBaseUrl: string,
  url: string
): Promise<void> {
  const res = await fetchWithToken(apiBaseUrl, { url, method: "DELETE" });
  if (!res.ok) {
    throw new Error(await readErrorText(res));
  }
}
