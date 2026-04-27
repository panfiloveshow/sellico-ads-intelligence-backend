import createClient from "openapi-fetch";
import type { paths } from "./schema.gen";

import { authTokenStore } from "@/lib/authTokens";

// In production both frontend and API share an origin (nginx serves the
// React bundle), so an empty baseUrl makes openapi-fetch use relative URLs.
// In dev, vite.config.ts proxies /api to http://localhost:8080.
const BASE_URL = import.meta.env.VITE_API_BASE_URL ?? "";

export const api = createClient<paths>({ baseUrl: BASE_URL });

// Auth interceptor: attach Bearer token; on 401 try a single refresh round
// and replay; on second 401 hand control back to the caller (which will
// route to /login via the router guard).
api.use({
  async onRequest({ request }) {
    const access = authTokenStore.access();
    if (access) {
      request.headers.set("Authorization", `Bearer ${access}`);
    }
    return request;
  },
  async onResponse({ request, response }) {
    if (response.status !== 401) return response;
    if (request.headers.get("X-Skip-Refresh") === "true") return response;

    const refreshed = await authTokenStore.refresh();
    if (!refreshed) return response;

    const replay = new Request(request, {
      headers: new Headers({
        ...Object.fromEntries(request.headers.entries()),
        Authorization: `Bearer ${refreshed}`,
        "X-Skip-Refresh": "true",
      }),
    });
    return fetch(replay);
  },
});
