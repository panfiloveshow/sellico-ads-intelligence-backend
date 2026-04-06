const DEFAULT_CONFIG = {
  backendUrl: "http://127.0.0.1:8080",
  accessToken: "",
  workspaceId: "",
  autoCapture: true
};

const SESSION_TTL_MS = 5 * 60 * 1000;
let activeSession = null;

async function getConfig() {
  const stored = await chrome.storage.sync.get(DEFAULT_CONFIG);
  return { ...DEFAULT_CONFIG, ...stored };
}

function looksLikeJWT(value) {
  if (!value) {
    return false;
  }
  const raw = String(value).trim();
  const parts = raw.split(".");
  if (parts.length !== 3) {
    return false;
  }
  return parts.every((p) => p.length >= 10);
}

async function getSellicoTokenFromCookies() {
  const candidates = await chrome.cookies.getAll({ domain: "sellico.ru" });
  for (const cookie of candidates) {
    if (!cookie?.value) {
      continue;
    }
    if (looksLikeJWT(cookie.value)) {
      return cookie.value.trim();
    }
    const normalized = cookie.value.trim();
    if (normalized.toLowerCase().startsWith("bearer ")) {
      const token = normalized.slice("bearer ".length).trim();
      if (looksLikeJWT(token)) {
        return token;
      }
    }
  }
  return "";
}

function normalizeBackendUrl(value) {
  return String(value || DEFAULT_CONFIG.backendUrl).replace(/\/+$/, "");
}

async function apiFetch(path, init = {}) {
  const config = await getConfig();
  let token = config.accessToken;
  if (!token) {
    token = await getSellicoTokenFromCookies();
  }
  if (!token || !config.workspaceId) {
    throw new Error("Sellico extension is not configured");
  }

  const headers = new Headers(init.headers || {});
  headers.set("Authorization", `Bearer ${token}`);
  headers.set("X-Workspace-ID", config.workspaceId);
  if (!headers.has("Content-Type") && init.body) {
    headers.set("Content-Type", "application/json");
  }

  const url = `${normalizeBackendUrl(config.backendUrl)}${path}`;
  let response = await fetch(url, { ...init, headers });

  // Retry once on 401 — session may have expired server-side
  if (response.status === 401 && !init._retried) {
    activeSession = null;
    const freshToken = await getSellicoTokenFromCookies();
    if (freshToken) {
      headers.set("Authorization", `Bearer ${freshToken}`);
    }
    response = await fetch(url, { ...init, headers, _retried: true });
  }

  if (!response.ok) {
    const text = await response.text().catch(() => "");
    // Sanitize error: don't leak backend internals to console
    const safeMsg = text.length > 200 ? text.slice(0, 200) + "..." : text;
    throw new Error(`Sellico API ${response.status}: ${safeMsg}`);
  }
  if (response.status === 204) {
    return null;
  }
  return response.json();
}

async function ensureSession() {
  const now = Date.now();
  if (activeSession && now-activeSession.startedAt < SESSION_TTL_MS) {
    return activeSession.data;
  }

  const payload = await apiFetch("/api/v1/extension/sessions", {
    method: "POST",
    body: JSON.stringify({
      extension_version: chrome.runtime.getManifest().version
    })
  });
  activeSession = {
    startedAt: now,
    data: payload?.data ?? payload
  };
  return activeSession.data;
}

function normalizeWidgetResponse(payload) {
  return payload?.data ?? payload;
}

async function fetchWidget(message) {
  console.log("[Sellico BG] fetchWidget message:", message);
  const params = new URLSearchParams();
  if (message.query) {
    params.set("query", message.query);
    const url = `/api/v1/extension/widgets/search?${params.toString()}`;
    console.log("[Sellico BG] Fetching:", url);
    const payload = await apiFetch(url);
    console.log("[Sellico BG] Search widget response:", payload);
    return normalizeWidgetResponse(payload);
  }
  if (message.wbProductId) {
    params.set("wb_product_id", String(message.wbProductId));
    const url = `/api/v1/extension/widgets/product?${params.toString()}`;
    console.log("[Sellico BG] Fetching:", url);
    const payload = await apiFetch(url);
    console.log("[Sellico BG] Product widget response:", payload);
    return normalizeWidgetResponse(payload);
  }
  if (message.productId) {
    params.set("product_id", String(message.productId));
    const url = `/api/v1/extension/widgets/product?${params.toString()}`;
    console.log("[Sellico BG] Fetching:", url);
    const payload = await apiFetch(url);
    console.log("[Sellico BG] Product widget response:", payload);
    return normalizeWidgetResponse(payload);
  }
  if (message.wbCampaignId) {
    params.set("wb_campaign_id", String(message.wbCampaignId));
    const url = `/api/v1/extension/widgets/campaign?${params.toString()}`;
    console.log("[Sellico BG] Fetching:", url);
    const payload = await apiFetch(url);
    console.log("[Sellico BG] Campaign widget response:", payload);
    return normalizeWidgetResponse(payload);
  }
  if (message.campaignId) {
    params.set("campaign_id", String(message.campaignId));
    const url = `/api/v1/extension/widgets/campaign?${params.toString()}`;
    console.log("[Sellico BG] Fetching:", url);
    const payload = await apiFetch(url);
    console.log("[Sellico BG] Campaign widget response:", payload);
    return normalizeWidgetResponse(payload);
  }
  console.log("[Sellico BG] No widget params matched, message:", message);
  return null;
}

async function handleIngest(path, body) {
  await ensureSession();
  const payload = await apiFetch(path, {
    method: "POST",
    body: JSON.stringify(body)
  });
  return normalizeWidgetResponse(payload);
}

chrome.runtime.onMessage.addListener((message, _sender, sendResponse) => {
  (async () => {
    switch (message?.type) {
      case "extension:get-config":
        sendResponse({ ok: true, config: await getConfig() });
        return;
      case "extension:start-session":
        sendResponse({ ok: true, session: await ensureSession() });
        return;
      case "extension:fetch-widget":
        sendResponse({ ok: true, widget: await fetchWidget(message) });
        return;
      case "extension:create-page-context":
        sendResponse({
          ok: true,
          result: await handleIngest("/api/v1/extension/page-context", message.payload)
        });
        return;
      case "extension:create-bid-snapshots":
        sendResponse({
          ok: true,
          result: await handleIngest("/api/v1/extension/bid-snapshots", { items: message.items || [] })
        });
        return;
      case "extension:create-position-snapshots":
        sendResponse({
          ok: true,
          result: await handleIngest("/api/v1/extension/position-snapshots", { items: message.items || [] })
        });
        return;
      case "extension:create-ui-signals":
        sendResponse({
          ok: true,
          result: await handleIngest("/api/v1/extension/ui-signals", { items: message.items || [] })
        });
        return;
      case "extension:create-network-captures":
        sendResponse({
          ok: true,
          result: await handleIngest("/api/v1/extension/network-captures/batch", { items: message.items || [] })
        });
        return;
      default:
        sendResponse({ ok: false, error: "Unknown message type" });
    }
  })().catch((error) => {
    sendResponse({ ok: false, error: error?.message || String(error) });
  });
  return true;
});
