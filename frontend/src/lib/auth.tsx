import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from "react";

import { authTokenStore } from "./authTokens";

interface UserSummary {
  id: string;
  email: string;
  workspace_id?: string;
}

interface AuthContextValue {
  user: UserSummary | null;
  loading: boolean;
  login: (email: string, password: string) => Promise<{ ok: true } | { ok: false; error: string }>;
  logout: () => Promise<void>;
}

const AuthContext = createContext<AuthContextValue | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<UserSummary | null>(null);
  const [loading, setLoading] = useState(true);

  // On first mount, try to restore the session via the refresh cookie.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      const access = await authTokenStore.refresh();
      if (cancelled) return;
      if (!access) {
        setLoading(false);
        return;
      }
      try {
        const resp = await fetch("/api/v1/auth/me", {
          headers: { Authorization: `Bearer ${access}` },
        });
        if (resp.ok) {
          const body = (await resp.json()) as { data: UserSummary };
          if (!cancelled) setUser(body.data);
        }
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const value = useMemo<AuthContextValue>(
    () => ({
      user,
      loading,
      async login(email, password) {
        const resp = await fetch("/api/v1/auth/login", {
          method: "POST",
          credentials: "include",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ email, password }),
        });
        if (!resp.ok) {
          const body = (await resp.json().catch(() => ({}))) as { errors?: { message?: string }[] };
          return { ok: false, error: body.errors?.[0]?.message ?? "Login failed" } as const;
        }
        const body = (await resp.json()) as { data: { access_token: string; user: UserSummary } };
        authTokenStore.set(body.data.access_token);
        setUser(body.data.user);
        return { ok: true } as const;
      },
      async logout() {
        await fetch("/api/v1/auth/logout", { method: "POST", credentials: "include" }).catch(() => {});
        authTokenStore.clear();
        setUser(null);
      },
    }),
    [user, loading],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used inside <AuthProvider>");
  return ctx;
}
