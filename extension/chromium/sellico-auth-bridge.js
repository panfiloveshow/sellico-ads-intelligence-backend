(function () {
  "use strict";

  const CONNECT_PARAM = "sellicoExtensionConnect";
  const TOKEN_KEYS = [
    "accessToken",
    "access_token",
    "authToken",
    "auth_token",
    "sellicoAccessToken",
    "sellico_access_token"
  ];
  const WORKSPACE_KEYS = [
    "currentWorkspaceId",
    "workspaceId",
    "activeWorkspaceId",
    "selectedWorkspaceId",
    "sellicoWorkspaceId"
  ];

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

  function looksLikeJWT(value) {
    const raw = normalizeAccessToken(value);
    const parts = raw.split(".");
    return parts.length === 3 && parts.every((part) => part.length >= 10);
  }

  function decodeBase64UrlJSON(value) {
    try {
      const normalized = String(value || "").replace(/-/g, "+").replace(/_/g, "/");
      const padded = normalized.padEnd(Math.ceil(normalized.length / 4) * 4, "=");
      return JSON.parse(window.atob(padded));
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

  function tokenCandidateScore(key, value) {
    if (!isUsableBearer(value)) {
      return -100;
    }
    const normalizedKey = String(key || "").toLowerCase();
    if (/(refresh|csrf|xsrf|session|remember|idtoken|id_token)/.test(normalizedKey)) {
      return -100;
    }
    let score = 0;
    if (/^(access_token|access-token|accesstoken|authtoken|auth_token|auth-token|sellicoaccesstoken|sellico_access_token)$/.test(normalizedKey)) {
      score += 100;
    }
    if (normalizedKey.includes("access")) {
      score += 45;
    }
    if (normalizedKey.includes("auth")) {
      score += 25;
    }
    if (normalizedKey.includes("token")) {
      score += 15;
    }
    if (normalizedKey.includes("jwt")) {
      score += 10;
    }
    return score;
  }

  function readStorage(storage, key) {
    try {
      return storage.getItem(key);
    } catch (_error) {
      return "";
    }
  }

  function parseJSON(value) {
    if (!value || typeof value !== "string") {
      return null;
    }
    try {
      return JSON.parse(value);
    } catch (_error) {
      return null;
    }
  }

  function collectNestedValues(input, candidateKeys, output, path = "", depth = 0) {
    if (!input || depth > 4) {
      return;
    }
    if (typeof input !== "object") {
      return;
    }
    for (const key of candidateKeys) {
      if (Object.prototype.hasOwnProperty.call(input, key) && input[key] != null) {
        const value = String(input[key]).trim();
        if (value) {
          output.push({ key: path ? `${path}.${key}` : key, value });
        }
      }
    }
    for (const [key, value] of Object.entries(input)) {
      collectNestedValues(value, candidateKeys, output, path ? `${path}.${key}` : key, depth + 1);
    }
  }

  function findFromKnownKeys(keys) {
    const candidates = [];
    for (const storage of [window.localStorage, window.sessionStorage]) {
      for (const key of keys) {
        const direct = readStorage(storage, key);
        if (direct) {
          candidates.push({ key, value: direct, score: tokenCandidateScore(key, direct) });
        }
      }
    }
    candidates.sort((a, b) => b.score - a.score);
    return candidates.find((candidate) => candidate.score > 0)?.value || "";
  }

  function findWorkspaceFromKnownKeys(keys) {
    for (const storage of [window.localStorage, window.sessionStorage]) {
      for (const key of keys) {
        const direct = readStorage(storage, key);
        if (direct) {
          return direct;
        }
      }
    }
    return "";
  }

  function detectAuthPayload() {
    const accessToken = normalizeAccessToken(findFromKnownKeys(TOKEN_KEYS));
    const workspaceId = String(findWorkspaceFromKnownKeys(WORKSPACE_KEYS) || "").trim();
    return {
      accessToken,
      workspaceId,
      backendUrl: "https://ads.sellico.ru",
      autoCapture: true
    };
  }

  function shouldAutoConnect() {
    try {
      const url = new URL(window.location.href);
      return url.searchParams.get(CONNECT_PARAM) === "1" || url.hash.includes(`${CONNECT_PARAM}=1`);
    } catch (_error) {
      return false;
    }
  }

  async function saveAuth({ loud = false } = {}) {
    const payload = detectAuthPayload();
    if (!payload.accessToken) {
      if (loud) {
        console.info("[Sellico] Жду авторизацию Sellico: действующий access token пока не найден");
      }
      return false;
    }

    const response = await chrome.runtime.sendMessage({
      type: "extension:save-auth-from-sellico",
      payload
    });
    if (!response?.ok) {
      throw new Error(response?.error || "Не удалось сохранить авторизацию Sellico");
    }
    if (response.config?.verified) {
      console.info("[Sellico] Авторизация расширения проверена", {
        workspaceId: response.config?.workspaceId,
        backendVersion: response.config?.verification?.version || "unknown"
      });
    } else {
      console.warn("[Sellico] Авторизация сохранена, но backend пока не принял подключение", {
        workspaceId: response.config?.workspaceId,
        message: response.config?.verification?.message || "нет деталей"
      });
    }
    if (loud) {
      showConnectedNotice(response.config?.verified);
    }
    return true;
  }

  function scheduleAuthCapture({ loud = false } = {}) {
    const delays = [500, 1500, 3000, 6000, 10000, 15000];
    let completed = false;
    delays.forEach((delay, index) => {
      window.setTimeout(() => {
        if (completed) {
          return;
        }
        saveAuth({ loud: loud && index === delays.length - 1 }).then((saved) => {
          completed = Boolean(saved);
        }).catch((error) => {
          console.warn("[Sellico] Не удалось подключить расширение автоматически", error);
        });
      }, delay);
    });
  }

  function showConnectedNotice(verified) {
    const existing = document.getElementById("sellico-extension-auth-notice");
    if (existing) {
      existing.remove();
    }
    const notice = document.createElement("div");
    notice.id = "sellico-extension-auth-notice";
    notice.textContent = verified
      ? "Расширение Sellico Live подключено. Теперь откройте кабинет Wildberries."
      : "Авторизация сохранена, но Ads API не подтвердил подключение. Откройте настройки расширения и нажмите «Проверить подключение».";
    notice.style.position = "fixed";
    notice.style.right = "20px";
    notice.style.bottom = "20px";
    notice.style.zIndex = "2147483647";
    notice.style.maxWidth = "360px";
    notice.style.padding = "14px 16px";
    notice.style.border = "1px solid rgba(37, 99, 235, 0.25)";
    notice.style.borderRadius = "14px";
    notice.style.background = "#ffffff";
    notice.style.boxShadow = "0 18px 55px rgba(15, 23, 42, 0.18)";
    notice.style.color = "#0f172a";
    notice.style.font = "600 14px/1.45 -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif";
    document.documentElement.appendChild(notice);
    window.setTimeout(() => notice.remove(), 8000);
  }

  const explicitConnect = shouldAutoConnect();
  if (explicitConnect) {
    scheduleAuthCapture({ loud: explicitConnect });
  }
})();
