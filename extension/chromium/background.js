const DEFAULT_PUBLIC_CONFIG = {
  backendUrl: "https://ads.sellico.ru",
  workspaceId: "",
  autoCapture: true
};
const DEFAULT_SECRET_CONFIG = {
  accessToken: ""
};
const LEGACY_LOCAL_BACKEND_URLS = new Set(["http://127.0.0.1:8080", "http://localhost:8080"]);
const SELLICO_AUTH_ORIGINS = new Set([
  "https://sellico.ru",
  "https://www.sellico.ru",
  "https://app.sellico.ru",
  "https://api.sellico.ru"
]);
const SELLICO_APP_API_URL = "https://sellico.ru/api";
const DEBUG_LOGS = false;

const SESSION_TTL_MS = 5 * 60 * 1000;
let activeSession = null;
let sessionPromise = null;

if (chrome.storage?.local?.setAccessLevel) {
  chrome.storage.local.setAccessLevel({ accessLevel: "TRUSTED_CONTEXTS" }).catch(() => {
    // Older Chromium builds may not support setAccessLevel; background remains the token vault.
  });
}

async function getConfig() {
  const publicStored = await chrome.storage.sync.get({ ...DEFAULT_PUBLIC_CONFIG, accessToken: "" });
  const secretStored = await chrome.storage.local.get(DEFAULT_SECRET_CONFIG);
  const config = {
    ...DEFAULT_PUBLIC_CONFIG,
    ...publicStored,
    accessToken: normalizeAccessToken(secretStored.accessToken || publicStored.accessToken || "")
  };

  if (publicStored.accessToken && !secretStored.accessToken) {
    await chrome.storage.local.set({ accessToken: normalizeAccessToken(publicStored.accessToken) });
    await chrome.storage.sync.remove("accessToken");
  }

  if (config.accessToken && looksLikeJWT(config.accessToken) && isExpiredJWT(config.accessToken)) {
    await clearStoredAccessToken();
    config.accessToken = "";
  }

  const backendUrl = normalizeBackendUrl(config.backendUrl);
  if (!config.accessToken && !config.workspaceId && LEGACY_LOCAL_BACKEND_URLS.has(backendUrl)) {
    config.backendUrl = DEFAULT_PUBLIC_CONFIG.backendUrl;
    await chrome.storage.sync.set({ backendUrl: config.backendUrl });
  } else {
    config.backendUrl = backendUrl;
  }
  return config;
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

function decodeBase64UrlJSON(value) {
  try {
    const normalized = String(value || "").replace(/-/g, "+").replace(/_/g, "/");
    const padded = normalized.padEnd(Math.ceil(normalized.length / 4) * 4, "=");
    return JSON.parse(atob(padded));
  } catch (_error) {
    return null;
  }
}

function jwtPayload(value) {
  const token = normalizeAccessToken(value);
  if (!looksLikeJWT(token)) {
    return null;
  }
  return decodeBase64UrlJSON(token.split(".")[1]);
}

function isExtensionToken(value) {
  const payload = jwtPayload(value);
  return payload?.type === "extension" && payload?.aud === "sellico-ads-extension";
}

function isExpiredJWT(value) {
  const payload = jwtPayload(value);
  if (!payload?.exp) {
    return false;
  }
  return Number(payload.exp) <= Math.floor(Date.now() / 1000) + 30;
}

function isUsableBearer(value) {
  const token = normalizeAccessToken(value);
  if (!token || token.length < 16) {
    return false;
  }
  if (looksLikeJWT(token)) {
    return !isExpiredJWT(token);
  }
  return !/[\s<>{}]/.test(token);
}

function normalizeAccessToken(value) {
  const raw = String(value || "").trim();
  if (!raw) {
    return "";
  }
  if (raw.toLowerCase().startsWith("bearer ")) {
    return raw.slice("bearer ".length).trim();
  }
  return raw;
}

function unwrapDataEnvelope(payload) {
  if (payload && typeof payload === "object" && payload.data && typeof payload.data === "object") {
    return payload.data;
  }
  return payload;
}

function extractSellicoAccessToken(payload) {
  const unwrapped = unwrapDataEnvelope(payload);
  return normalizeAccessToken(
    unwrapped?.access_token ||
      unwrapped?.accessToken ||
      unwrapped?.token ||
      unwrapped?.auth_token ||
      unwrapped?.credentials?.access_token ||
      ""
  );
}

function debugLog(...args) {
  if (DEBUG_LOGS) {
    console.debug(...args);
  }
}

async function clearStoredAccessToken() {
  await chrome.storage.local.remove("accessToken");
  await chrome.storage.sync.remove("accessToken");
  activeSession = null;
}

async function exchangeSellicoTokenForExtensionToken({ backendUrl, workspaceId, sellicoAccessToken }) {
  const token = normalizeAccessToken(sellicoAccessToken);
  const normalizedBackendUrl = normalizeBackendUrl(backendUrl || DEFAULT_PUBLIC_CONFIG.backendUrl);
  const normalizedWorkspaceId = normalizeWorkspaceId(workspaceId);
  if (!isUsableBearer(token) || !normalizedWorkspaceId) {
    throw new Error("Не хватает Sellico token или workspace для выпуска токена расширения.");
  }

  const response = await fetch(`${normalizedBackendUrl}/api/v1/extension/token/exchange`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${token}`,
      "X-Workspace-ID": normalizedWorkspaceId,
      Accept: "application/json",
      "Content-Type": "application/json"
    },
    body: JSON.stringify({})
  });
  const payload = await response.json().catch(() => null);
  if (!response.ok) {
    const text = payload ? JSON.stringify(payload).slice(0, 240) : "";
    throw new Error(`Sellico API не выпустил токен расширения (${response.status})${text ? `: ${text}` : ""}`);
  }
  const data = unwrapDataEnvelope(payload);
  const extensionToken = extractSellicoAccessToken(data);
  const issuedWorkspaceId = normalizeWorkspaceId(data?.workspace_id || data?.workspaceId || normalizedWorkspaceId);
  if (!isUsableBearer(extensionToken) || !isExtensionToken(extensionToken)) {
    throw new Error("Sellico API вернул некорректный токен расширения.");
  }
  return {
    accessToken: extensionToken,
    workspaceId: issuedWorkspaceId
  };
}

function normalizeBackendUrl(value) {
  return String(value || DEFAULT_PUBLIC_CONFIG.backendUrl).replace(/\/+$/, "");
}

function publicConfig(config) {
  return {
    backendUrl: config.backendUrl,
    autoCapture: config.autoCapture,
    hasAccessToken: Boolean(config.accessToken),
    hasWorkspaceId: Boolean(config.workspaceId)
  };
}

function isTrustedSellicoSender(sender) {
  try {
    const origin = new URL(sender?.url || "").origin;
    return SELLICO_AUTH_ORIGINS.has(origin);
  } catch (_error) {
    return false;
  }
}

function normalizeWorkspaceId(value) {
  return String(value || "").trim();
}

function workspaceIdFromCandidate(candidate) {
  if (!candidate || typeof candidate !== "object") {
    return "";
  }
  return normalizeWorkspaceId(
    candidate.id ||
      candidate.uuid ||
      candidate.workspace_id ||
      candidate.workspaceId ||
      candidate.external_id ||
      candidate.externalId ||
      ""
  );
}

function flattenWorkspaceCandidates(payload) {
  if (!payload) {
    return [];
  }
  if (Array.isArray(payload)) {
    return payload;
  }
  const direct = payload.data || payload.items || payload.workspaces || payload.results;
  if (Array.isArray(direct)) {
    return direct;
  }
  if (direct && typeof direct === "object") {
    const nested = direct.data || direct.items || direct.workspaces || direct.results;
    if (Array.isArray(nested)) {
      return nested;
    }
  }
  return [];
}

function pickWorkspaceId(workspaces) {
  const candidates = workspaces
    .map((workspace) => ({
      workspace,
      id: workspaceIdFromCandidate(workspace),
      preferred:
        Boolean(workspace?.is_current) ||
        Boolean(workspace?.current) ||
        Boolean(workspace?.selected) ||
        Boolean(workspace?.is_selected) ||
        Boolean(workspace?.active) ||
        Boolean(workspace?.is_active)
    }))
    .filter((item) => item.id);
  const preferred = candidates.find((item) => item.preferred);
  if (preferred) {
    return preferred.id;
  }
  return candidates[0]?.id || "";
}

async function loadSellicoWorkspaces(accessToken) {
  const token = normalizeAccessToken(accessToken);
  if (!token) {
    return [];
  }
  try {
    const response = await fetch(`${SELLICO_APP_API_URL}/workspaces`, {
      headers: {
        Authorization: `Bearer ${token}`,
        Accept: "application/json"
      }
    });
    if (!response.ok) {
      return [];
    }
    const payload = await response.json().catch(() => null);
    return flattenWorkspaceCandidates(payload);
  } catch (_error) {
    return [];
  }
}

async function loginToSellico(payload) {
  const email = String(payload?.email || "").trim();
  const password = String(payload?.password || "");
  if (!email || !password) {
    throw new Error("Введите email и пароль Sellico.");
  }

  let loginPayload = null;
  let responseStatus = 0;
  try {
    const response = await fetch(`${SELLICO_APP_API_URL}/login`, {
      method: "POST",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json"
      },
      body: JSON.stringify({ email, password })
    });
    responseStatus = response.status;
    loginPayload = await response.json().catch(() => null);
    if (!response.ok) {
      if (response.status === 401 || response.status === 422) {
        throw new Error("Sellico не принял email или пароль.");
      }
      throw new Error(`Sellico login вернул статус ${response.status}.`);
    }
  } catch (error) {
    if (error?.message) {
      throw error;
    }
    throw new Error("Не удалось подключиться к Sellico login.");
  }

  const accessToken = extractSellicoAccessToken(loginPayload);
  if (!isUsableBearer(accessToken)) {
    throw new Error(`Sellico login не вернул действующий access token${responseStatus ? ` (status ${responseStatus})` : ""}.`);
  }

  return saveAuthFromSellico(
    {
      accessToken,
      workspaceId: payload?.workspaceId,
      backendUrl: payload?.backendUrl || DEFAULT_PUBLIC_CONFIG.backendUrl,
      autoCapture: payload?.autoCapture !== false
    },
    { url: "https://sellico.ru/extension-login" }
  );
}

async function discoverWorkspaceIdFromSellico(accessToken, preferredWorkspaceId = "") {
  const workspaces = await loadSellicoWorkspaces(accessToken);
  const preferred = normalizeWorkspaceId(preferredWorkspaceId);
  if (preferred) {
    const matched = workspaces.some((workspace) => workspaceIdFromCandidate(workspace) === preferred);
    if (!matched) {
      return "";
    }
    await chrome.storage.sync.set({ workspaceId: preferred });
    return preferred;
  }
  try {
    const workspaceId = pickWorkspaceId(workspaces);
    if (workspaceId) {
      await chrome.storage.sync.set({ workspaceId });
    }
    return workspaceId;
  } catch (_error) {
    return "";
  }
}

async function saveAuthFromSellico(payload, sender) {
  if (!isTrustedSellicoSender(sender)) {
    throw new Error("Источник авторизации Sellico не доверен");
  }

  const accessToken = normalizeAccessToken(payload?.accessToken);
  let workspaceId = normalizeWorkspaceId(payload?.workspaceId);

  if (!isUsableBearer(accessToken)) {
    throw new Error("На странице Sellico не найден действующий accessToken. Войдите в Sellico заново и повторите подключение.");
  }
  workspaceId = await discoverWorkspaceIdFromSellico(accessToken, workspaceId);
  if (!workspaceId) {
    throw new Error("Workspace не подтвержден для текущей авторизации Sellico. Откройте Sellico CRM или выберите workspace в настройках расширения.");
  }

  const backendUrl = normalizeBackendUrl(payload?.backendUrl || DEFAULT_PUBLIC_CONFIG.backendUrl);
  const exchanged = await exchangeSellicoTokenForExtensionToken({
    backendUrl,
    workspaceId,
    sellicoAccessToken: accessToken
  });
  workspaceId = exchanged.workspaceId || workspaceId;

  await chrome.storage.local.set({ accessToken: exchanged.accessToken });
  await chrome.storage.sync.set({
    backendUrl,
    workspaceId,
    autoCapture: payload?.autoCapture !== false
  });
  await chrome.storage.sync.remove("accessToken");
  activeSession = null;

  const verification = await testStoredConnection();
  if (!verification.connected) {
    await clearStoredAccessToken();
    throw new Error(verification.message || "Sellico API не подтвердил авторизацию расширения. Войдите в Sellico заново.");
  }

  return {
    backendUrl,
    workspaceId,
    hasAccessToken: true,
    verified: verification.connected,
    verification
  };
}

async function apiFetch(path, init = {}) {
  const config = await getConfig();
  let token = normalizeAccessToken(config.accessToken);
  if (token && looksLikeJWT(token) && isExpiredJWT(token)) {
    await clearStoredAccessToken();
    token = "";
  }
  let workspaceId = normalizeWorkspaceId(config.workspaceId);
  if (token && !workspaceId && !isExtensionToken(token)) {
    workspaceId = await discoverWorkspaceIdFromSellico(token);
  }
  if (token && workspaceId && !isExtensionToken(token)) {
    const exchanged = await exchangeSellicoTokenForExtensionToken({
      backendUrl: config.backendUrl,
      workspaceId,
      sellicoAccessToken: token
    });
    token = exchanged.accessToken;
    workspaceId = exchanged.workspaceId || workspaceId;
    await chrome.storage.local.set({ accessToken: token });
    await chrome.storage.sync.set({ workspaceId });
    await chrome.storage.sync.remove("accessToken");
  }
  if (!token || !workspaceId) {
    throw new Error("Расширение Sellico не подключено: укажите Backend URL, Workspace ID и Access token в настройках расширения");
  }

  const headers = new Headers(init.headers || {});
  headers.set("Authorization", `Bearer ${token}`);
  headers.set("X-Workspace-ID", workspaceId);
  if (!headers.has("Content-Type") && init.body) {
    headers.set("Content-Type", "application/json");
  }

  const url = `${normalizeBackendUrl(config.backendUrl)}${path}`;
  let response = await fetch(url, { ...init, headers });

  if (response.status === 401) {
    activeSession = null;
    await clearStoredAccessToken();
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

async function testStoredConnection() {
  const config = await getConfig();
  const token = config.accessToken;
  let workspaceId = normalizeWorkspaceId(config.workspaceId);
  if (token && !workspaceId) {
    workspaceId = await discoverWorkspaceIdFromSellico(token);
  }
  if (!token || !workspaceId) {
    return {
      connected: false,
      stage: "config",
      message: "Расширение не подключено: нет access token или workspace.",
      config: publicConfig(config)
    };
  }

  try {
    const versionPayload = await apiFetch("/api/v1/extension/version");
    const sessionPayload = await apiFetch("/api/v1/extension/sessions", {
      method: "POST",
      body: JSON.stringify({
        extension_version: chrome.runtime.getManifest().version
      })
    });
    const evidenceSummary = await fetchEvidenceSummary();
    return {
      connected: true,
      stage: "ready",
      version: versionPayload?.data?.version || versionPayload?.version || "unknown",
      sessionId: sessionPayload?.data?.id || sessionPayload?.id || "",
      evidenceSummary,
      config: publicConfig(await getConfig())
    };
  } catch (error) {
    return {
      connected: false,
      stage: "api",
      message: error?.message || String(error),
      config: publicConfig(await getConfig())
    };
  }
}

async function ensureSession() {
  const now = Date.now();
  if (activeSession && now - activeSession.startedAt < SESSION_TTL_MS) {
    return activeSession.data;
  }

  // Prevent concurrent session creation — reuse in-flight request
  if (sessionPromise) {
    return sessionPromise;
  }

  sessionPromise = (async () => {
    const payload = await apiFetch("/api/v1/extension/sessions", {
      method: "POST",
      body: JSON.stringify({
        extension_version: chrome.runtime.getManifest().version
      })
    });
    activeSession = {
      startedAt: Date.now(),
      data: payload?.data ?? payload
    };
    return activeSession.data;
  })().finally(() => {
    sessionPromise = null;
  });

  return sessionPromise;
}

function normalizeWidgetResponse(payload) {
  return payload?.data ?? payload;
}

async function fetchWidget(message) {
  debugLog("[Sellico BG] fetchWidget message:", message);
  const params = new URLSearchParams();
  if (message.query) {
    params.set("query", message.query);
    const url = `/api/v1/extension/widgets/search?${params.toString()}`;
    debugLog("[Sellico BG] Fetching:", url);
    const payload = await apiFetch(url);
    debugLog("[Sellico BG] Search widget response:", payload);
    return normalizeWidgetResponse(payload);
  }
  if (message.wbProductId) {
    params.set("wb_product_id", String(message.wbProductId));
    const url = `/api/v1/extension/widgets/product?${params.toString()}`;
    debugLog("[Sellico BG] Fetching:", url);
    const payload = await apiFetch(url);
    debugLog("[Sellico BG] Product widget response:", payload);
    return normalizeWidgetResponse(payload);
  }
  if (message.productId) {
    params.set("product_id", String(message.productId));
    const url = `/api/v1/extension/widgets/product?${params.toString()}`;
    debugLog("[Sellico BG] Fetching:", url);
    const payload = await apiFetch(url);
    debugLog("[Sellico BG] Product widget response:", payload);
    return normalizeWidgetResponse(payload);
  }
  if (message.wbCampaignId) {
    params.set("wb_campaign_id", String(message.wbCampaignId));
    const url = `/api/v1/extension/widgets/campaign?${params.toString()}`;
    debugLog("[Sellico BG] Fetching:", url);
    const payload = await apiFetch(url);
    debugLog("[Sellico BG] Campaign widget response:", payload);
    return normalizeWidgetResponse(payload);
  }
  if (message.campaignId) {
    params.set("campaign_id", String(message.campaignId));
    const url = `/api/v1/extension/widgets/campaign?${params.toString()}`;
    debugLog("[Sellico BG] Fetching:", url);
    const payload = await apiFetch(url);
    debugLog("[Sellico BG] Campaign widget response:", payload);
    return normalizeWidgetResponse(payload);
  }
  debugLog("[Sellico BG] No widget params matched", message);
  return null;
}

async function fetchEvidenceSummary() {
	try {
		const payload = await apiFetch("/api/v1/extension/evidence-summary");
		return normalizeWidgetResponse(payload);
	} catch (error) {
    return {
      unavailable: true,
      message: error?.message || String(error)
    };
	}
}

async function fetchEvidenceDebug(message) {
	const params = new URLSearchParams();
	if (message.scope) {
		params.set("scope", message.scope);
	}
	if (message.campaignId) {
		params.set("campaign_id", String(message.campaignId));
	}
	if (message.productId) {
		params.set("product_id", String(message.productId));
	}
	if (message.phraseId) {
		params.set("phrase_id", String(message.phraseId));
	}
	if (message.query) {
		params.set("query", String(message.query));
	}
	params.set("limit", String(message.limit || 10));
	const payload = await apiFetch(`/api/v1/extension/evidence-debug?${params.toString()}`);
	return normalizeWidgetResponse(payload);
}

async function fetchEvidenceSupportReport(message) {
	const params = new URLSearchParams();
	if (message.scope) {
		params.set("scope", message.scope);
	}
	if (message.campaignId) {
		params.set("campaign_id", String(message.campaignId));
	}
	if (message.productId) {
		params.set("product_id", String(message.productId));
	}
	if (message.phraseId) {
		params.set("phrase_id", String(message.phraseId));
	}
	if (message.query) {
		params.set("query", String(message.query));
	}
	params.set("limit", String(message.limit || 20));
	const payload = await apiFetch(`/api/v1/extension/evidence-debug/report?${params.toString()}`);
	return normalizeWidgetResponse(payload);
}

async function handleIngest(path, body) {
	await ensureSession();
	const payload = await apiFetch(path, {
    method: "POST",
    body: JSON.stringify(body)
  });
  return normalizeWidgetResponse(payload);
}

chrome.action.onClicked.addListener(() => {
  chrome.runtime.openOptionsPage().catch(() => {});
});

chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  (async () => {
    switch (message?.type) {
      case "extension:get-config":
        sendResponse({ ok: true, config: publicConfig(await getConfig()) });
        return;
      case "extension:save-auth-from-sellico":
        sendResponse({ ok: true, config: await saveAuthFromSellico(message.payload, sender) });
        return;
      case "extension:login-sellico":
        sendResponse({ ok: true, config: await loginToSellico(message.payload) });
        return;
      case "extension:test-connection":
        sendResponse({ ok: true, result: await testStoredConnection() });
        return;
      case "extension:open-options":
        await chrome.runtime.openOptionsPage();
        sendResponse({ ok: true });
        return;
      case "extension:start-session":
        sendResponse({ ok: true, session: await ensureSession() });
        return;
      case "extension:fetch-widget":
        sendResponse({ ok: true, widget: await fetchWidget(message) });
        return;
		case "extension:fetch-evidence-summary":
			sendResponse({ ok: true, summary: await fetchEvidenceSummary() });
			return;
		case "extension:fetch-evidence-debug":
			sendResponse({ ok: true, debug: await fetchEvidenceDebug(message) });
			return;
		case "extension:fetch-evidence-support-report":
			sendResponse({ ok: true, report: await fetchEvidenceSupportReport(message) });
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
      case "extension:create-dom-row-snapshots":
        sendResponse({
          ok: true,
          result: await handleIngest("/api/v1/extension/dom-row-snapshots", { items: message.items || [] })
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
