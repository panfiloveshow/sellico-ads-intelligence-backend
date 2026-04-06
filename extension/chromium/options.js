const DEFAULT_CONFIG = {
  backendUrl: "http://127.0.0.1:8080",
  accessToken: "",
  workspaceId: "",
  autoCapture: true
};

async function loadConfig() {
  const config = await chrome.storage.sync.get(DEFAULT_CONFIG);
  document.getElementById("backendUrl").value = config.backendUrl || DEFAULT_CONFIG.backendUrl;
  document.getElementById("accessToken").value = config.accessToken || "";
  document.getElementById("workspaceId").value = config.workspaceId || "";
  document.getElementById("autoCapture").checked = Boolean(config.autoCapture);
}

async function saveConfig(event) {
  event.preventDefault();
  const nextConfig = {
    backendUrl: document.getElementById("backendUrl").value.trim(),
    accessToken: document.getElementById("accessToken").value.trim(),
    workspaceId: document.getElementById("workspaceId").value.trim(),
    autoCapture: document.getElementById("autoCapture").checked
  };
  const statusEl = document.getElementById("status");
  if (!nextConfig.backendUrl) {
    statusEl.textContent = "Укажи Backend URL.";
    return;
  }
  if (!nextConfig.workspaceId) {
    statusEl.textContent = "Укажи Workspace ID (внешний Sellico workspace/account id).";
    return;
  }
  await chrome.storage.sync.set(nextConfig);
  statusEl.textContent = "Настройки сохранены.";
}

async function testConnection() {
  const statusEl = document.getElementById("status");
  const backendUrl = document.getElementById("backendUrl").value.trim();
  const accessToken = document.getElementById("accessToken").value.trim();
  const workspaceId = document.getElementById("workspaceId").value.trim();

  if (!backendUrl) {
    statusEl.textContent = "Укажи Backend URL для проверки.";
    statusEl.style.color = "#dc2626";
    return;
  }

  statusEl.textContent = "Проверяю подключение...";
  statusEl.style.color = "#475467";

  try {
    const headers = { "Content-Type": "application/json" };
    if (accessToken) {
      headers["Authorization"] = `Bearer ${accessToken}`;
    }
    if (workspaceId) {
      headers["X-Workspace-ID"] = workspaceId;
    }

    const url = backendUrl.replace(/\/+$/, "");
    const response = await fetch(`${url}/health/ready`, { headers, signal: AbortSignal.timeout(5000) });

    if (response.ok) {
      statusEl.textContent = "Подключение успешно! Backend доступен.";
      statusEl.style.color = "#16a34a";
    } else {
      statusEl.textContent = `Backend вернул ${response.status}. Проверь URL и токен.`;
      statusEl.style.color = "#dc2626";
    }
  } catch (error) {
    statusEl.textContent = `Не удалось подключиться: ${error.message || "сеть недоступна"}`;
    statusEl.style.color = "#dc2626";
  }
}

document.getElementById("settings-form").addEventListener("submit", saveConfig);
document.getElementById("test-connection").addEventListener("click", testConnection);
loadConfig();
