/**
 * Per-user application settings client for the Wails frontend — mirrors
 * src/frontend/spa/src/utils/api.ts.
 *
 * These call GET/POST /api/settings and GET/POST /api/mcpdir, backed by
 * routers/settings.go and routers/mcpdir.go in the Go backend. All endpoints
 * are guarded by requireAuth at the mux, so they need a valid
 * "Authorization: Bearer <jwt>" header. The token is injected here via
 * getToken() from ./token, mirroring the SPA's fetchWithToken behavior.
 *
 * NOTE: the Wails frontend currently operates without auth, so getToken() may
 * return null and the backend will respond 401 on these endpoints. When auth
 * is enabled, the token must be stored via setToken() in ./token before these
 * helpers are called. Until then, requests are sent without a token.
 */

import { getToken } from "./token";

// SettingsResponse is the client-facing view of a user's settings as returned
// by GET/POST /api/settings. context_limit is the decoded ctxLimit (the
// per-user context token budget; defaults to 50000 when unset), and raw holds
// every stored key/value pair (values always strings).
export interface SettingsResponse {
  context_limit: number;
  raw: Record<string, string>;
}

/**
 * mcpdirResponse is the client-facing view of a user's MCP working directory as
 * returned by GET/POST /api/mcpdir. The value is the raw, relative, ".."-free
 * selector stored by the user (e.g. "mcp_data"), empty when unset.
 */
export interface McpdirResponse {
  value: string;
}

/** Returns the caller's decoded settings (context_limit) plus the raw map. */
export async function getSettings(apiBaseUrl: string): Promise<SettingsResponse> {
  const res = await fetch(`${apiBaseUrl}/api/settings`, { method: "GET" });
  if (!res.ok) {
    throw new Error(`HTTP ${res.status}`);
  }
  return (await res.json()) as SettingsResponse;
}

/** Sets a single setting for the caller (only the "ctxLimit" key is accepted). */
export async function setSetting(
  apiBaseUrl: string,
  payload: { key: string; value: string }
): Promise<SettingsResponse> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
  };
  const token = getToken();
  if (token) {
    headers["Authorization"] = `Bearer ${token}`;
  }
  const res = await fetch(`${apiBaseUrl}/api/settings`, {
    method: "POST",
    headers,
    body: JSON.stringify(payload),
  });
  if (!res.ok) {
    throw new Error(`HTTP ${res.status}`);
  }
  return (await res.json()) as SettingsResponse;
}

/** Returns the caller's current MCP working-directory selector. */
export async function getMcpdir(apiBaseUrl: string): Promise<McpdirResponse> {
  const res = await fetch(`${apiBaseUrl}/api/mcpdir`, { method: "GET" });
  if (!res.ok) {
    throw new Error(`HTTP ${res.status}`);
  }
  return (await res.json()) as McpdirResponse;
}

/** Sets (or clears, when value is empty) the caller's MCP working directory. */
export async function setMcpdir(
  apiBaseUrl: string,
  payload: { value: string }
): Promise<McpdirResponse> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
  };
  const token = getToken();
  if (token) {
    headers["Authorization"] = `Bearer ${token}`;
  }
  const res = await fetch(`${apiBaseUrl}/api/mcpdir`, {
    method: "POST",
    headers,
    body: JSON.stringify(payload),
  });
  if (!res.ok) {
    const detail = await res.json().catch(() => ({}));
    throw new Error(
      (detail && detail.detail) || `HTTP ${res.status}`
    );
  }
  return (await res.json()) as McpdirResponse;
}
