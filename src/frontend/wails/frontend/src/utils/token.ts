// Token storage for the Wails frontend, mirroring the SPA's auth token
// persistence. The backend requires a valid "Authorization: Bearer <jwt>"
// header on the API endpoints that authenticate the caller (see
// routers.requireAuth in backend-go). The token is stored in memory only and
// never persisted, matching the SPA's behavior.

let token: string | null = null;

export function getToken(): string | null {
  return token;
}

/** Stores the bearer token after a successful login (mirrors SPA). */
export function setToken(t: string | null): void {
  token = t;
}
