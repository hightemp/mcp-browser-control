import { enabledPermissionProfileNames } from "./permission-profiles.js";

const STATUS_PRESENTATION = Object.freeze({
  connected: Object.freeze({ color: "#15803d", text: "ON" }),
  connecting: Object.freeze({ color: "#ca8a04", text: "…" }),
  handshaking: Object.freeze({ color: "#ca8a04", text: "…" }),
  pairing_required: Object.freeze({ color: "#c2410c", text: "PAIR" }),
  disconnected: Object.freeze({ color: "#64748b", text: "OFF" }),
  error: Object.freeze({ color: "#b91c1c", text: "!" }),
});

export function badgeForStatus(status) {
  return STATUS_PRESENTATION[status] || STATUS_PRESENTATION.disconnected;
}

export function permissionProfilesFor(permissions = {}) {
  return enabledPermissionProfileNames(permissions);
}
