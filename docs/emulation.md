# Reversible Tab Emulation

Status: implemented typed CDP emulation with explicit and detach cleanup

Last reviewed: 2026-08-24

The MCP `full` profile exposes three tab-scoped tools:

- `browser_set_emulation` replaces the complete managed configuration;
- `browser_get_emulation_state` returns the configuration owned by this
  extension session; and
- `browser_reset_emulation` clears every managed override and releases its CDP
  lease.

Set and get require Observe site access, Debug permission, an HTTP(S) top-level
document, and optional document identity for stale-target rejection. Reset is a
tab-only cleanup command. It still requires Debug permission but deliberately
bypasses target-origin policy and site access so a navigation cannot make
cleanup impossible.

## Supported Settings

| Setting group | Supported values and bounds |
| --- | --- |
| `viewport` | width/height 1–10,000 CSS pixels, device scale 0.1–10, mobile mode, and fixed primary/secondary portrait/landscape orientation |
| `touch` | enabled state and at most 10 touch points |
| `network` | offline state, latency 0–300,000 ms, upload/download 0–10,000,000 Kbit/s, and a fixed CDP connection-type enum |
| `userAgent` | bounded User-Agent, accept-language, and platform strings with control characters rejected |
| `locale` | bounded ICU-style locale identifiers |
| `timezoneId` | bounded IANA-style timezone identifiers; the browser remains the authority on actual support |
| `geolocation` | bounded latitude, longitude, accuracy, altitude, heading, and speed; this does not grant a site's geolocation permission |
| `media` | screen/print plus fixed color-scheme, reduced-motion, forced-colors, and contrast preferences |

Omitted groups are cleared because set is replacement, not patch, semantics.
The returned `applied` array names every active group. User-Agent Client Hint
metadata, arbitrary media-feature names, CPU throttling, script disabling,
sensors, idle state, and experimental screen/posture controls are not exposed.

## CDP Boundary

One long-running consumer lease is shared through the CDP Session Manager. Its
exact allowlist is:

- `Emulation.setDeviceMetricsOverride` and
  `Emulation.clearDeviceMetricsOverride`;
- `Emulation.setTouchEmulationEnabled`;
- `Emulation.setUserAgentOverride`;
- `Emulation.setLocaleOverride` and `Emulation.setTimezoneOverride`;
- `Emulation.setGeolocationOverride` and
  `Emulation.clearGeolocationOverride`;
- `Emulation.setEmulatedMedia`; and
- `Network.emulateNetworkConditions`.

The Network command is deprecated in the tip-of-tree protocol, but remains the
compatibility path for the supported Chrome/Edge 116+ range. A future migration
to `emulateNetworkConditionsByRule` plus `overrideNetworkState` must be
version-gated and preserve the same typed schema and reset behavior.

Kbit/s input is converted to CDP bytes per second. The extension does not enable
Network event capture, inspect requests, or expose response bodies as part of
emulation.

## Replacement and Cleanup

Operations are serialized independently per tab. Before applying a replacement,
the handler clears device metrics and geolocation and sends neutral values for
touch, network, User-Agent, locale, timezone, and media. It then applies only
the requested groups. A failure or cancellation triggers the same complete
best-effort reset and releases the lease, so partial configurations are not
retained intentionally.

Successful overrides persist across navigation in the addressed tab until
explicit reset. Reset repeats the complete neutral sequence before releasing
the lease, which matters when another consumer keeps the shared debugger
attachment alive. External debugger detach, tab closure, Debug permission
revocation, extension disconnect, and MCP WebSocket closure invalidate the
lease; Chrome's debugger-session teardown is the final browser-owned reset
boundary. The extension also discards its state immediately on detach.

State is held in extension memory and reports only values supplied through the
typed tool. It is not a probe for host locale, location, timezone, or network
configuration.

## Verification Boundary

Automated tests cover strict schemas and bounds, full-profile gating, exact CDP
methods, unit conversion, orientation and media mapping, replace/get/reset,
per-tab serialization, partial-failure rollback, external detach, missing Debug
permission, and cleanup without target-origin access. Release validation still
requires real Chrome and Edge smoke tests for every setting group, cross-origin
navigation, DevTools conflict, permission revocation, and disconnect cleanup.

Method and parameter definitions come from the official Chrome DevTools
Protocol [Emulation](https://chromedevtools.github.io/devtools-protocol/tot/Emulation/)
and [Network](https://chromedevtools.github.io/devtools-protocol/tot/Network/)
domains.
