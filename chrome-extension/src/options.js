import {
  permissionProfileStates,
  permissionRemovalFor,
  permissionRequestFor,
} from "./permission-profiles.js";

const elements = {
  form: document.querySelector("#settings-form"),
  status: document.querySelector("#status"),
  displayName: document.querySelector("#display-name"),
  endpoint: document.querySelector("#endpoint"),
  autoConnect: document.querySelector("#auto-connect"),
  pageAutomation: document.querySelector("#page-automation"),
  retry: document.querySelector("#retry"),
  refresh: document.querySelector("#refresh"),
  browserRuntime: document.querySelector("#browser-runtime"),
  browserId: document.querySelector("#browser-id"),
  connectionId: document.querySelector("#connection-id"),
  diagnosticEndpoint: document.querySelector("#diagnostic-endpoint"),
  lastConnected: document.querySelector("#last-connected"),
  latency: document.querySelector("#latency"),
  permissionProfiles: document.querySelector("#permission-profiles"),
  permissionProfileList: document.querySelector("#permission-profile-list"),
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
        featureFlags: {
          pageAutomation: elements.pageAutomation.checked,
        },
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
  elements.pageAutomation.checked = data.settings?.featureFlags?.pageAutomation !== false;
  elements.browserRuntime.textContent = [data.browserName, data.browserVersion]
    .filter(Boolean)
    .join(" ") || "Chromium";
  elements.browserId.textContent = data.browserId || "—";
  elements.connectionId.textContent = data.connectionId || "—";
  elements.diagnosticEndpoint.textContent = data.settings?.endpoint || "—";
  elements.lastConnected.textContent = formatDate(data.lastConnectedAt);
  elements.latency.textContent = Number.isFinite(data.latencyMS) ? `${data.latencyMS} ms` : "—";
  elements.permissionProfiles.textContent = data.permissionProfiles?.join(", ") || "Core";
  renderPermissionProfiles(data.permissions);
  elements.capabilities.textContent = data.capabilities?.join(", ") || "None";
  elements.permissions.textContent = [
    ...(data.permissions?.permissions || []),
    ...(data.permissions?.origins || []),
  ].join(", ") || "None";
  showError(data.error || "");
}

function renderPermissionProfiles(permissions) {
  const cards = permissionProfileStates(permissions).map((profile) => {
    const card = document.createElement("article");
    card.className = "profile-card";

    const heading = document.createElement("div");
    heading.className = "profile-heading";
    const title = document.createElement("h3");
    title.textContent = profile.name;
    const status = document.createElement("span");
    status.className = `profile-status ${profile.state}`;
    status.textContent = profile.state;
    heading.append(title, status);

    const description = document.createElement("p");
    description.textContent = profile.description;
    const access = document.createElement("p");
    if (profile.origins.length > 0) {
      access.textContent = [
        `Available hosts: ${profile.origins.join(", ")}.`,
        `Granted hosts: ${profile.grantedOrigins.join(", ") || "none"}.`,
      ].join(" ");
    } else {
      access.textContent = `Permissions: ${profile.permissions.join(", ") || "Chrome APIs without extra grants"}`;
    }
    const tools = document.createElement("p");
    tools.textContent = `Related tools: ${profile.tools.join(", ")}`;
    const warning = document.createElement("p");
    warning.textContent = profile.warning;
    const lifecycle = document.createElement("p");
    lifecycle.textContent = "Applied immediately; no tab reload or server reconnect is required.";

    card.append(heading, description, access, tools, warning, lifecycle);
    if (profile.optional) {
      const actions = document.createElement("div");
      actions.className = "profile-actions";
      const toggle = document.createElement("button");
      toggle.type = "button";
      toggle.textContent = profile.state === "enabled" ? "Disable" : "Enable";
      toggle.className = profile.state === "enabled" ? "secondary" : "";
      toggle.addEventListener("click", () => {
        void run(async () => {
          if (profile.state === "enabled") {
            await changePermissions("remove", permissionRemovalFor(profile.id));
          } else {
            await changePermissions("request", permissionRequestFor(profile.id));
          }
          await refresh();
        });
      });
      actions.append(toggle);
      if (profile.state === "partial") {
        const remove = document.createElement("button");
        remove.type = "button";
        remove.className = "secondary";
        remove.textContent = "Remove grants";
        remove.addEventListener("click", () => {
          void run(async () => {
            await changePermissions("remove", permissionRemovalFor(profile.id));
            await refresh();
          });
        });
        actions.append(remove);
      }
      card.append(actions);
    }
    return card;
  });
  elements.permissionProfileList.replaceChildren(...cards);
}

async function changePermissions(operation, request) {
  const changed = await chrome.permissions[operation](request);
  if (!changed) {
    throw new Error(operation === "request"
      ? "Permission request was declined"
      : "The permission profile could not be removed");
  }
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
