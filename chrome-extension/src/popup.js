const elements = {
  status: document.querySelector("#status"),
  browserId: document.querySelector("#browser-id"),
  displayName: document.querySelector("#display-name"),
  endpoint: document.querySelector("#endpoint"),
  autoConnect: document.querySelector("#auto-connect"),
  error: document.querySelector("#error"),
  save: document.querySelector("#save"),
  connect: document.querySelector("#connect"),
  disconnect: document.querySelector("#disconnect"),
  grantAccess: document.querySelector("#grant-access"),
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

elements.grantAccess.addEventListener("click", () => {
  void run(async () => {
    const granted = await chrome.permissions.request({
      origins: ["http://*/*", "https://*/*"],
    });
    if (!granted) {
      throw new Error("Website access was not granted");
    }
    await refresh();
  });
});

chrome.runtime.onMessage.addListener((message) => {
  if (message?.type === "CONNECTION_STATUS_CHANGED") {
    renderStatus(message.data.status);
    showError(message.data.error || "");
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
  elements.browserId.textContent = data.browserId || "—";
  elements.browserId.title = data.browserId || "";
  elements.displayName.value = data.settings?.displayName || "";
  elements.endpoint.value = data.settings?.endpoint || "";
  elements.autoConnect.checked = Boolean(data.settings?.autoConnect);
}

function renderStatus(status) {
  elements.status.textContent = status || "unknown";
  elements.status.className = `status ${status || ""}`;
}

function showError(message) {
  elements.error.hidden = !message;
  elements.error.textContent = message;
}
