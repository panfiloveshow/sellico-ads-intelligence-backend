const DEFAULT_PUBLIC_CONFIG = {
  backendUrl: "https://ads.sellico.ru",
  workspaceId: "",
  autoCapture: true
};
const DEFAULT_SECRET_CONFIG = {
  accessToken: ""
};
const LEGACY_LOCAL_BACKEND_URLS = new Set(["http://127.0.0.1:8080", "http://localhost:8080"]);

async function loadConfig() {
  const publicConfig = await chrome.storage.sync.get({ ...DEFAULT_PUBLIC_CONFIG, accessToken: "" });
  const secretConfig = await chrome.storage.local.get(DEFAULT_SECRET_CONFIG);
  const config = {
    ...DEFAULT_PUBLIC_CONFIG,
    ...publicConfig,
    accessToken: normalizeAccessToken(secretConfig.accessToken || publicConfig.accessToken || "")
  };
  if (publicConfig.accessToken && !secretConfig.accessToken) {
    await chrome.storage.local.set({ accessToken: normalizeAccessToken(publicConfig.accessToken) });
    await chrome.storage.sync.remove("accessToken");
  }
  const normalizedBackendUrl = normalizeBackendUrl(config.backendUrl);
  if (!config.accessToken && !config.workspaceId && LEGACY_LOCAL_BACKEND_URLS.has(normalizedBackendUrl)) {
    config.backendUrl = DEFAULT_PUBLIC_CONFIG.backendUrl;
    await chrome.storage.sync.set({ backendUrl: config.backendUrl });
  }
  document.getElementById("backendUrl").value = config.backendUrl || DEFAULT_PUBLIC_CONFIG.backendUrl;
  document.getElementById("accessToken").value = config.accessToken || "";
  document.getElementById("workspaceId").value = config.workspaceId || "";
  document.getElementById("autoCapture").checked = Boolean(config.autoCapture);
}

async function savePublicSettings({ preserveToken = true } = {}) {
  const backendUrl = normalizeBackendUrl(document.getElementById("backendUrl").value);
  if (!backendUrl) {
    throw new Error("Укажите Backend URL.");
  }
  const permissionGranted = await ensureBackendHostPermission(backendUrl);
  if (!permissionGranted) {
    throw new Error("Chrome не дал разрешение на этот Backend URL.");
  }

  await chrome.storage.sync.set({
    backendUrl,
    workspaceId: document.getElementById("workspaceId").value.trim(),
    autoCapture: document.getElementById("autoCapture").checked
  });

  const accessToken = normalizeAccessToken(document.getElementById("accessToken").value);
  if (accessToken) {
    await chrome.storage.local.set({ accessToken });
    await chrome.storage.sync.remove("accessToken");
  } else if (!preserveToken) {
    await chrome.storage.local.remove("accessToken");
    await chrome.storage.sync.remove("accessToken");
  }
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

function normalizeBackendUrl(value) {
  return String(value || DEFAULT_PUBLIC_CONFIG.backendUrl).trim().replace(/\/+$/, "");
}

function permissionOriginForUrl(value) {
  try {
    const url = new URL(normalizeBackendUrl(value));
    return `${url.protocol}//${url.host}/*`;
  } catch (_error) {
    return "";
  }
}

async function ensureBackendHostPermission(backendUrl) {
  const origin = permissionOriginForUrl(backendUrl);
  if (!origin || origin.startsWith("https://sellico.ru/") || origin.startsWith("https://api.sellico.ru/")) {
    return true;
  }
  const hasPermission = await chrome.permissions.contains({ origins: [origin] });
  if (hasPermission) {
    return true;
  }
  return chrome.permissions.request({ origins: [origin] });
}

function setStatus(message, tone = "info") {
  const statusEl = document.getElementById("status");
  statusEl.textContent = message;
  statusEl.dataset.tone = tone;
}

function setSupportStatus(message, tone = "info") {
  const statusEl = document.getElementById("supportStatus");
  statusEl.textContent = message;
  statusEl.dataset.tone = tone;
}

function escapeHtml(value) {
  return String(value ?? "")
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

function supportText(value, fallback = "нет данных") {
  const text = String(value ?? "").trim();
  return text || fallback;
}

async function saveConfig(event) {
  event.preventDefault();
  const nextConfig = {
    backendUrl: normalizeBackendUrl(document.getElementById("backendUrl").value),
    accessToken: normalizeAccessToken(document.getElementById("accessToken").value),
    workspaceId: document.getElementById("workspaceId").value.trim(),
    autoCapture: document.getElementById("autoCapture").checked
  };
  if (!nextConfig.backendUrl) {
    setStatus("Укажите Backend URL.", "error");
    return;
  }
  if (!nextConfig.workspaceId) {
    setStatus("Укажите Workspace ID.", "error");
    return;
  }
  const permissionGranted = await ensureBackendHostPermission(nextConfig.backendUrl);
  if (!permissionGranted) {
    setStatus("Chrome не дал разрешение на этот Backend URL. Подключение не сохранено.", "error");
    return;
  }
  await chrome.storage.sync.set({
    backendUrl: nextConfig.backendUrl,
    workspaceId: nextConfig.workspaceId,
    autoCapture: nextConfig.autoCapture
  });
  if (nextConfig.accessToken) {
    await chrome.storage.local.set({ accessToken: nextConfig.accessToken });
    await chrome.storage.sync.remove("accessToken");
  }
  setStatus("Настройки сохранены. Авторизация не сброшена. Теперь откройте страницу кампании в кабинете WB.", "success");
}

async function testConnection() {
  setStatus("Проверяю подключение...", "info");

  try {
    await savePublicSettings({ preserveToken: true });
    const response = await chrome.runtime.sendMessage({ type: "extension:test-connection" });
    if (!response?.ok) {
      setStatus(response?.error || "Не удалось проверить подключение.", "error");
      return;
    }

    const result = response.result || {};
    if (result.connected) {
      const evidence = result.evidenceSummary || {};
      const evidenceText = evidence && !evidence.unavailable
        ? ` Сигналы: сеть ${evidence.network_captures || 0}, ставки ${evidence.bid_snapshots || 0}, позиции ${evidence.position_snapshots || 0}, предупреждения ${evidence.ui_signals || 0}.`
        : "";
      setStatus(`Подключение успешно. Extension API доступен, backend ${result.version || "unknown"}.${evidenceText}`, "success");
    } else {
      setStatus(result.message || "Подключение не прошло проверку.", "error");
    }
  } catch (error) {
    setStatus(`Не удалось подключиться: ${error.message || "сеть недоступна"}`, "error");
  }
}

async function loginViaSellico() {
  const email = document.getElementById("sellicoEmail").value.trim();
  const password = document.getElementById("sellicoPassword").value;
  const backendUrl = normalizeBackendUrl(document.getElementById("backendUrl").value);
  const preferredWorkspaceId = document.getElementById("workspaceId").value.trim();

  if (!email || !password) {
    setStatus("Введите email и пароль Sellico.", "error");
    return;
  }
  const permissionGranted = await ensureBackendHostPermission(backendUrl);
  if (!permissionGranted) {
    setStatus("Chrome не дал разрешение на Backend URL. Авторизация не запущена.", "error");
    return;
  }

  setStatus("Вхожу в Sellico и проверяю workspace...", "info");
  try {
    await chrome.storage.sync.set({
      backendUrl,
      workspaceId: preferredWorkspaceId,
      autoCapture: document.getElementById("autoCapture").checked
    });

    const response = await chrome.runtime.sendMessage({
      type: "extension:login-sellico",
      payload: {
        email,
        password,
        backendUrl,
        workspaceId: preferredWorkspaceId,
        autoCapture: document.getElementById("autoCapture").checked
      }
    });

    if (!response?.ok) {
      setStatus(response?.error || "Sellico не подтвердил авторизацию.", "error");
      return;
    }

    document.getElementById("workspaceId").value = response.config?.workspaceId || preferredWorkspaceId;
    document.getElementById("accessToken").value = "";
    document.getElementById("sellicoPassword").value = "";
    setStatus(`Подключено. Backend ${response.config?.verification?.version || "unknown"}, workspace ${response.config?.workspaceId || "подтвержден"}.`, "success");
  } catch (error) {
    setStatus(`Не удалось войти: ${error.message || "ошибка сети"}`, "error");
  }
}

async function connectViaSellico() {
  const backendUrl = normalizeBackendUrl(document.getElementById("backendUrl").value);
  const permissionGranted = await ensureBackendHostPermission(backendUrl);
  if (!permissionGranted) {
    setStatus("Chrome не дал разрешение на Backend URL. Авторизация не запущена.", "error");
    return;
  }
  await chrome.storage.sync.set({
    backendUrl,
    autoCapture: document.getElementById("autoCapture").checked
  });
  const connectUrl = "https://sellico.ru/ads-intelligence?sellicoExtensionConnect=1#sellicoExtensionConnect=1";
  await chrome.tabs.create({ url: connectUrl, active: true });
  setStatus("Открыл Sellico. Текущая авторизация расширения сохранена до успешного нового подключения.", "info");
}

async function resetAuth() {
  await chrome.storage.local.remove("accessToken");
  await chrome.storage.sync.remove(["accessToken", "workspaceId"]);
  document.getElementById("accessToken").value = "";
  document.getElementById("workspaceId").value = "";
  setStatus("Авторизация расширения сброшена. Нажмите «Подключить через Sellico» и войдите заново.", "info");
}

function supportReportRequest() {
  const scope = document.getElementById("supportScope").value;
  const limit = Number(document.getElementById("supportLimit").value || 20);
  const request = {
    type: "extension:fetch-evidence-support-report",
    scope,
    limit: Number.isFinite(limit) ? Math.min(Math.max(limit, 1), 50) : 20
  };
  const campaignId = document.getElementById("supportCampaignId").value.trim();
  const productId = document.getElementById("supportProductId").value.trim();
  const phraseId = document.getElementById("supportPhraseId").value.trim();
  const query = document.getElementById("supportQuery").value.trim();
  if (campaignId) {
    request.campaignId = campaignId;
  }
  if (productId) {
    request.productId = productId;
  }
  if (phraseId) {
    request.phraseId = phraseId;
  }
  if (query) {
    request.query = query;
  }
  return request;
}

function validateSupportReportRequest(request) {
  if (request.scope === "campaign" && !request.campaignId) {
    return "Для campaign укажите Campaign ID.";
  }
  if (request.scope === "product" && !request.productId) {
    return "Для product укажите Product ID.";
  }
  if (request.scope === "query" && !request.query && !request.phraseId) {
    return "Для query укажите Query или Phrase ID.";
  }
  return "";
}

function renderSupportReport(report) {
  const target = document.getElementById("supportResult");
  const summary = report.summary || {};
  const sections = Array.isArray(report.sections) ? report.sections : [];
  const checklist = Array.isArray(report.checklist) ? report.checklist : [];
  const issues = Array.isArray(report.issues) ? report.issues : [];
  const summaryItems = [
    ["Источник", supportText(summary.source_label)],
    ["Готовность", supportText(summary.readiness)],
    ["Captured", `${Number(summary.captured_signals || 0)} / ${Number(summary.captured_signals || 0) + Number(summary.missing_signals || 0)}`],
    ["Freshness", supportText(summary.freshness_state)]
  ];

  target.innerHTML = `
    <div class="support-summary">
      ${summaryItems.map(([label, value]) => `
        <div class="metric">
          <span>${escapeHtml(label)}</span>
          <strong>${escapeHtml(value)}</strong>
        </div>
      `).join("")}
    </div>

    <div class="section-title">
      <strong>Evidence sections</strong>
      <small>Секции отражают только реальные сохраненные captures для выбранной сущности.</small>
    </div>
    <ul class="support-list">
      ${sections.map((item) => `
        <li>
          <span class="badge" data-status="${escapeHtml(item.status)}">${escapeHtml(supportText(item.status))}</span>
          <span><strong>${escapeHtml(item.title)}</strong><br>${escapeHtml(item.detail)}</span>
          <span>${Number(item.evidence_count || 0)}</span>
        </li>
      `).join("") || "<li><span>Нет секций evidence.</span></li>"}
    </ul>

    <div class="section-title">
      <strong>Checklist</strong>
      <small>Что support-команде нужно открыть или обновить, если evidence неполный.</small>
    </div>
    <ul class="support-list">
      ${checklist.map((item) => `
        <li>
          <span class="badge" data-status="${Boolean(item.done)}">${item.done ? "done" : "todo"}</span>
          <span><strong>${escapeHtml(item.label)}</strong><br>${escapeHtml(item.detail)}</span>
          <span>${escapeHtml(item.action_path || "")}</span>
        </li>
      `).join("") || "<li><span>Checklist пуст.</span></li>"}
    </ul>

    ${issues.length ? `
      <div class="section-title">
        <strong>Issues</strong>
        <small>Ошибки и предупреждения приходят из backend evidence status.</small>
      </div>
      <ul class="support-list">
        ${issues.map((item) => `
          <li>
            <span class="badge" data-status="${escapeHtml(item.severity)}">${escapeHtml(item.severity || "issue")}</span>
            <span><strong>${escapeHtml(item.stage)}</strong><br>${escapeHtml(item.message)}</span>
            <span>${escapeHtml(item.action_path || "")}</span>
          </li>
        `).join("")}
      </ul>
    ` : ""}
  `;
  target.hidden = false;
}

async function loadSupportReport(event) {
  event.preventDefault();
  const request = supportReportRequest();
  const validationError = validateSupportReportRequest(request);
  if (validationError) {
    setSupportStatus(validationError, "error");
    return;
  }

  setSupportStatus("Загружаю support evidence report...", "info");
  document.getElementById("supportResult").hidden = true;

  try {
    await savePublicSettings({ preserveToken: true });
    const response = await chrome.runtime.sendMessage(request);
    if (!response?.ok) {
      setSupportStatus(response?.error || "Не удалось загрузить evidence report.", "error");
      return;
    }
    const report = response.report || {};
    renderSupportReport(report);
    const summary = report.summary || {};
    setSupportStatus(`Report загружен: ${supportText(summary.readiness)} / ${supportText(summary.source_label)}.`, "success");
  } catch (error) {
    setSupportStatus(`Не удалось загрузить report: ${error.message || "сеть недоступна"}`, "error");
  }
}

document.getElementById("settings-form").addEventListener("submit", saveConfig);
document.getElementById("support-form").addEventListener("submit", loadSupportReport);
document.getElementById("login-sellico").addEventListener("click", loginViaSellico);
document.getElementById("test-connection").addEventListener("click", testConnection);
document.getElementById("connect-sellico").addEventListener("click", connectViaSellico);
document.getElementById("reset-auth").addEventListener("click", resetAuth);
chrome.storage.onChanged.addListener((changes, areaName) => {
  if (areaName === "sync" && (changes.workspaceId || changes.backendUrl || changes.autoCapture)) {
    loadConfig();
    if (changes.workspaceId) {
      setStatus("Авторизация Sellico сохранена. Теперь нажмите «Проверить подключение».", "success");
    }
  }
});
loadConfig();
