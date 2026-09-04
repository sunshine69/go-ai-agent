import { useCallback, useEffect, useState } from "react";
import { getJSON, postJSON } from "../utils/api";
import {
  AuthUser,
  clearToken,
  clearUser,
  getToken,
  getUser,
  saveToken,
  saveUser,
} from "../utils/token";

export interface LoginRequest {
  username: string;
  password: string;
}

export interface LoginResponse {
  access_token: string;
  token_type: string;
  user: AuthUser;
}

interface MeResponse {
  login_name: string;
  email: string;
  is_admin: boolean;
}

export interface Auth {
  /** The logged-in user, or null when signed out / unknown. */
  user: AuthUser | null;
  /** Whether a session token exists locally. */
  isAuthenticated: boolean;
  /** Whether the initial session has been resolved from storage. */
  initialized: boolean;
  /** Attempt to log in. Throws with a backend message on failure. */
  login: (req: LoginRequest) => Promise<void>;
  /** Register a new account and immediately log in. Throws on failure. */
  register: (
    loginName: string,
    email: string,
    password: string
  ) => Promise<void>;
  /** Log out on the backend and clear local session. */
  logout: () => Promise<void>;
  /** Load the current profile from the backend and update `user`. */
  refresh: () => Promise<void>;
}

export function useAuth(apiBaseUrl: string, onUnauthorized?: () => void): Auth {
  const [user, setUser] = useState<AuthUser | null>(getUser());
  const [initialized, setInitialized] = useState(false);

  // Seed the session from localStorage on mount. A stored token means the user
  // is (still) logged in until the backend proves the token expired.
  useEffect(() => {
    const maybeToken = getToken();
    if (maybeToken) {
      const existing = getUser();
      if (existing) {
        setUser(existing);
        void refresh(existing);
      } else {
        // Token present but we lost the user record — verify server-side.
        const fake = { id: 0, login_name: "", email: "", is_admin: false } as AuthUser;
        setUser(fake);
        void refresh(fake);
      }
    }
    setInitialized(true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const login = useCallback(async (req: LoginRequest) => {
    const resp = await postJSON<LoginResponse>(apiBaseUrl, "/api/auth/login", {
      username: req.username,
      password: req.password,
    });
    saveToken(resp.access_token);
    saveUser(resp.user);
    setUser(resp.user);
  }, [apiBaseUrl]);

  const register = useCallback(async (loginName: string, email: string, password: string) => {
    const resp = await postJSON<LoginResponse>(apiBaseUrl, "/api/auth/register", {
      login_name: loginName,
      email,
      password,
    });
    saveToken(resp.access_token);
    saveUser(resp.user);
    setUser(resp.user);
  }, [apiBaseUrl]);

  const logout = useCallback(async () => {
    await logoutOnBackend(apiBaseUrl).catch(() => undefined);
    clearToken();
    clearUser();
    setUser(null);
  }, [apiBaseUrl]);

  const refresh = useCallback(
    async (fallback?: AuthUser) => {
      try {
        const me = await getJSON<MeResponse>(apiBaseUrl, "/api/auth/me");
        setUser((prev) => ({
          id: prev?.id ?? fallback?.id ?? 0,
          login_name: me.login_name,
          email: me.email,
          is_admin: me.is_admin,
          created_at: prev?.created_at ?? "",
          last_login: prev?.last_login,
        }));
      } catch (err) {
        // Session is invalid — drop it and give the caller a chance to react
        // (e.g. bounce to the login screen).
        clearToken();
        clearUser();
        setUser(null);
        onUnauthorized?.();
        throw err;
      }
    },
    [apiBaseUrl, onUnauthorized]
  );

  return {
    user,
    isAuthenticated: user !== null,
    initialized,
    login,
    register,
    logout,
    refresh,
  };
}

// Isolated logout POST (does not itself require auth) so logout always works.
async function logoutOnBackend(apiBaseUrl: string): Promise<void> {
  const res = await fetch(`${apiBaseUrl}/api/auth/logout`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
  });
  if (res.ok) {
    try {
      await res.json();
    } catch {
      /* ignore */
    }
  }
}
