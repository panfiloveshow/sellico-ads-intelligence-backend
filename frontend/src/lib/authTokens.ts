// Token storage strategy:
//
// Access token: in-memory only, lost on tab refresh — minimises XSS impact
//   (the long-lived refresh token does the heavy lifting).
// Refresh token: HttpOnly cookie set by the backend's /api/v1/auth/login
//   response (Sec-Set-Cookie). Browsers attach it automatically; JS can't
//   read it. We never touch it from this code.
//
// On a 401, the api client calls refresh() which POSTs to /api/v1/auth/refresh
// (no Authorization header — server reads the cookie). On success it returns
// the new access token; on failure it clears in-memory state and returns null
// so the caller can route to /login.

let accessToken: string | null = null;
let refreshInFlight: Promise<string | null> | null = null;

const REFRESH_PATH = "/api/v1/auth/refresh";

export const authTokenStore = {
  access(): string | null {
    return accessToken;
  },
  set(token: string | null) {
    accessToken = token;
  },
  /**
   * Single-flighted refresh: many 401s arriving in parallel coalesce into
   * one network call to /auth/refresh.
   */
  async refresh(): Promise<string | null> {
    if (refreshInFlight) return refreshInFlight;
    refreshInFlight = (async () => {
      try {
        const resp = await fetch(REFRESH_PATH, {
          method: "POST",
          credentials: "include",
        });
        if (!resp.ok) {
          accessToken = null;
          return null;
        }
        const body = (await resp.json()) as { data?: { access_token?: string } };
        const next = body.data?.access_token ?? null;
        accessToken = next;
        return next;
      } catch {
        accessToken = null;
        return null;
      } finally {
        refreshInFlight = null;
      }
    })();
    return refreshInFlight;
  },
  clear() {
    accessToken = null;
  },
};
