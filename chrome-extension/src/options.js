const elements = {
  form: document.querySelector("#settings-form"),
  status: document.querySelector("#status"),
  displayName: document.querySelector("#display-name"),
  endpoint: document.querySelector("#endpoint"),
  autoConnect: document.querySelector("#auto-connect"),
  retry: document.querySelector("#retry"),
  refresh: document.querySelector("#refresh"),
  browserRuntime: document.querySelector("#browser-runtime"),
  browserId: document.querySelector("#browser-id"),
  connectionId: document.querySelector("#connection-id"),
  diagnosticEndpoint: document.querySelector("#diagnostic-endpoint"),
  lastConnected: document.querySelector("#last-connected"),
  latency: document.querySelector("#latency"),
  permissionProfiles: document.querySelector("#permission-profiles"),
  capabilities: document.querySelector("#capabilities"),
  permissions: document.querySelector("#permissions"),
  error: document.querySelector("#error"),
};

elements.form.addEventListener("submit", (event) => {
  event.preventDefault();
  void run(async () => {
    await renderResponse(await chrome.runtime.sendMessage({
      type: "SAVE_SETTINGS",
      settings: {
        displayName: elements.displayName.value,
        endpoint: elements.endpoint.value,
        autoConnect: elements.autoConnect.checked,
      },
    }));
  });
});

elements.retry.addEventListener("click", () => {
  void run(async () => renderResponse(await chrome.runtime.sendMessage({ type: "CONNECT" })));
});

elements.refresh.addEventListener("click", () => {
  void refresh();
});

chrome.runtime.onMessage.addListener((message) => {
  if (message?.type === "CONNECTION_STATUS_CHANGED") {
    void refresh();
  }
});

void refresh();

async function refresh() {
  await run(async () => renderResponse(await chrome.runtime.sendMessage({ type: "GET_STATUS" })));
}

async function run(operation) {
  try {
    showError("");
    await operation();
  } catch (error) {
    showError(error.message || "Operation failed");
  }
}

async function renderResponse(response) {
  if (!response?.success) {
    showError(response?.error?.message || "Extension operation failed");
    return;
  }
  const data = response.data;
  elements.status.textContent = data.status || "unknown";
  elements.status.className = `status ${data.status || ""}`;
  elements.displayName.value = data.settings?.displayName || "";
  elements.endpoint.value = data.settings?.endpoint || "";
  elements.autoConnect.checked = Boolean(data.settings?.autoConnect);
  elements.browserRuntime.textContent = [data.browserName, data.browserVersion]
    .filter(Boolean)
    .join(" ") || "Chromium";
  elements.browserId.textContent = data.browserId || "—";
  elements.connectionId.textContent = data.connectionId || "—";
  elements.diagnosticEndpoint.textContent = data.settings?.endpoint || "—";
  elements.lastConnected.textContent = formatDate(data.lastConnectedAt);
  elements.latency.textContent = Number.isFinite(data.latencyMS) ? `${data.latencyMS} ms` : "—";
  elements.permissionProfiles.textContent = data.permissionProfiles?.join(", ") || "Core";
  elements.capabilities.textContent = data.capabilities?.join(", ") || "None";
  elements.permissions.textContent = [
    ...(data.permissions?.permissions || []),
    ...(data.permissions?.origins || []),
  ].join(", ") || "None";
  showError(data.error || "");
}

function formatDate(value) {
  if (!value) {
    return "Never";
  }
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "Unknown" : date.toLocaleString();
}

function showError(message) {
  elements.error.hidden = !message;
  elements.error.textContent = message;
}
