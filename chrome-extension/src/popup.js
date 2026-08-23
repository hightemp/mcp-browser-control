const elements = {
  status: document.querySelector("#status"),
  browserRuntime: document.querySelector("#browser-runtime"),
  browserId: document.querySelector("#browser-id"),
  connectionId: document.querySelector("#connection-id"),
  displayName: document.querySelector("#display-name"),
  endpoint: document.querySelector("#endpoint"),
  autoConnect: document.querySelector("#auto-connect"),
  pairingCode: document.querySelector("#pairing-code"),
  paired: document.querySelector("#paired"),
  latency: document.querySelector("#latency"),
  lastConnected: document.querySelector("#last-connected"),
  permissionProfiles: document.querySelector("#permission-profiles"),
  error: document.querySelector("#error"),
  save: document.querySelector("#save"),
  connect: document.querySelector("#connect"),
  disconnect: document.querySelector("#disconnect"),
  grantAccess: document.querySelector("#grant-access"),
  pair: document.querySelector("#pair"),
  revokePairing: document.querySelector("#revoke-pairing"),
  resetIdentity: document.querySelector("#reset-identity"),
};

elements.save.addEventListener("click", () => {
  void run(async () => {
    const response = await chrome.runtime.sendMessage({
      type: "SAVE_SETTINGS",
      settings: {
        displayName: elements.displayName.value,
        endpoint: elements.endpoint.value,
        autoConnect: elements.autoConnect.checked,
      },
    });
    renderResponse(response);
  });
});

elements.connect.addEventListener("click", () => {
  void run(async () => renderResponse(await chrome.runtime.sendMessage({ type: "CONNECT" })));
});

elements.disconnect.addEventListener("click", () => {
  void run(async () => renderResponse(await chrome.runtime.sendMessage({ type: "DISCONNECT" })));
});

elements.pair.addEventListener("click", () => {
  void run(async () => {
    const response = await chrome.runtime.sendMessage({
      type: "PAIR",
      pairingCode: elements.pairingCode.value,
    });
    renderResponse(response);
    if (response?.success) {
      elements.pairingCode.value = "";
    }
  });
});

elements.revokePairing.addEventListener("click", () => {
  void run(async () => {
    renderResponse(await chrome.runtime.sendMessage({ type: "REVOKE_PAIRING" }));
  });
});

elements.resetIdentity.addEventListener("click", () => {
  const confirmed = window.confirm(
    "Reset browser identity? This deletes the local pairing credential and requires pairing again. Revoke the current pairing first if you want to remove its server-side credential.",
  );
  if (!confirmed) {
    return;
  }
  void run(async () => {
    renderResponse(await chrome.runtime.sendMessage({ type: "RESET_IDENTITY", confirm: true }));
  });
});

elements.grantAccess.addEventListener("click", () => {
  void chrome.runtime.openOptionsPage();
});

chrome.runtime.onMessage.addListener((message) => {
  if (message?.type === "CONNECTION_STATUS_CHANGED") {
    renderStatus(message.data.status);
    showError(message.data.error || "");
    void refresh();
  }
});

void refresh();

async function refresh() {
  renderResponse(await chrome.runtime.sendMessage({ type: "GET_STATUS" }));
}

async function run(operation) {
  try {
    showError("");
    await operation();
  } catch (error) {
    showError(error.message || "Operation failed");
  }
}

function renderResponse(response) {
  if (!response?.success) {
    showError(response?.error?.message || "Extension operation failed");
    return;
  }
  const data = response.data;
  renderStatus(data.status);
  showError(data.error || "");
  elements.browserRuntime.textContent = [data.browserName, data.browserVersion]
    .filter(Boolean)
    .join(" ") || "Chromium";
  elements.browserId.textContent = data.browserId || "—";
  elements.browserId.title = data.browserId || "";
  elements.connectionId.textContent = data.connectionId || "—";
  elements.connectionId.title = data.connectionId || "";
  elements.displayName.value = data.settings?.displayName || "";
  elements.endpoint.value = data.settings?.endpoint || "";
  elements.autoConnect.checked = Boolean(data.settings?.autoConnect);
  elements.paired.textContent = data.paired ? "Yes" : "No";
  elements.latency.textContent = Number.isFinite(data.latencyMS) ? `${data.latencyMS} ms` : "—";
  elements.lastConnected.textContent = formatDate(data.lastConnectedAt);
  elements.permissionProfiles.textContent = data.permissionProfiles?.join(", ") || "Core";
  elements.revokePairing.disabled = !data.paired;
}

function formatDate(value) {
  if (!value) {
    return "Never";
  }
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "Unknown" : date.toLocaleString();
}

function renderStatus(status) {
  elements.status.textContent = status || "unknown";
  elements.status.className = `status ${status || ""}`;
}

function showError(message) {
  elements.error.hidden = !message;
  elements.error.textContent = message;
}
