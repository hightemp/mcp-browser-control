# Browser Support

Last reviewed: 2026-08-24

MCP Browser Control v1 targets desktop Chromium browsers with Manifest V3. The
runtime capability list, not the product name alone, is authoritative for each
connected profile.

## Support Matrix

| Browser | Minimum | Support tier | Release validation |
| --- | ---: | --- | --- |
| Google Chrome Stable | 116 | Supported | Automated two-profile Chrome for Testing E2E on every CI run |
| Microsoft Edge Stable | 116 | Supported | Unpacked-install and capability smoke test before release |
| Chromium | 116 | Compatible | Automated Chrome for Testing coverage; distribution-specific differences are best effort |
| Brave, Opera, Vivaldi, and other Chromium products | Chromium 116 base | Best effort | Browser identity and capability negotiation only; no release gate |
| Firefox | N/A | Not supported in v1 | Requires a separate manifest, API adapter, debugger design, and E2E suite |
| Mobile Chromium browsers | N/A | Not supported | Desktop extension APIs and a local server are required |

Chrome's extension documentation states that Manifest V3 is generally
available from Chrome 88 and that individual APIs may impose later minimums.
This project deliberately sets `minimum_chrome_version` to 116 because Chrome
116 added the service-worker WebSocket lifetime behavior required by the
connection keepalive. Older Chrome versions cannot install the packaged
extension and capability detection also fails closed below 116. See the
[Chrome extension API reference](https://developer.chrome.com/docs/extensions/reference/api),
[minimum version manifest key](https://developer.chrome.com/docs/extensions/reference/manifest/minimum-chrome-version),
and [service-worker WebSocket guidance](https://developer.chrome.com/docs/extensions/how-to/web-platform/websockets).

Microsoft documents Chrome extension APIs and manifest keys as code-compatible
with Edge, while warning that individual API parity can differ. The required
Core and optional APIs used by this project are listed for desktop Edge MV3 in
Microsoft's [supported API matrix](https://learn.microsoft.com/en-us/microsoft-edge/extensions/developer-guide/api-support)
and the general porting policy is described in
[Port a Chrome extension to Microsoft Edge](https://learn.microsoft.com/en-us/microsoft-edge/extensions-chromium/developer-guide/port-chrome-extension).

The minimum is a compatibility floor, not an update recommendation. Supported
installations should stay on their browser's current Stable or managed Extended
Stable channel for security updates. The exact Chrome for Testing version used
by CI is recorded in each job log instead of being frozen in this document.

## Capability Matrix

Every extension sends its detected API surface, granted permissions, and
feature flags during the authenticated handshake and whenever permissions
change. The server rejects a command that is not currently advertised.

| Capability domain | Required browser surface | Permission profile | Chrome 116+ | Edge 116+ | Other Chromium 116+ |
| --- | --- | --- | --- | --- | --- |
| Connection and `browser.ping` | MV3 service worker, WebSocket, alarms, storage | Core | Required | Required | Best effort |
| Windows and tabs | `windows`, `tabs` | Core | Required | Required | Best effort |
| Group/ungroup tabs | `tabs.group`, `tabs.ungroup` | Core API; MCP `full` profile | Required when API exists | Required when API exists | Capability-gated |
| Update tab-group presentation | `tabGroups.update` | Personal data (`tabGroups`) | Capability-gated | Capability-gated | Capability-gated |
| Recently closed and restore | `sessions` | Personal data (`sessions`) | Capability-gated | Capability-gated | Capability-gated |
| Page inspection and actions | `scripting`, `webNavigation`, HTTP/HTTPS host access | Observe plus Core | Capability-gated | Capability-gated | Capability-gated |
| Viewport screenshot | `tabs.captureVisibleTab`, target-origin access | Observe plus Core | Capability-gated | Capability-gated | Capability-gated |
| Console and page-error capture | packaged MAIN/ISOLATED bridges; optional managed `Runtime`/`Log`/`Network` enrichment | Observe plus Core; Debug is optional | Capability-gated | Capability-gated; CDP release smoke required | Bridge baseline only until CDP enrichment is tested |
| Network idle observation | `webRequest` and target-origin access | Observe | Capability-gated per wait | Capability-gated per wait | Capability-gated |
| CDP session infrastructure | `debugger`; flat child sessions require browser 125+ | Debug plus MCP `full` profile | Root manager implemented | Root manager implemented; release smoke still required | Not supported until tested |
| Print to PDF | managed `Page.printToPDF` | Observe + Debug plus MCP `full` profile | Capability-gated | Capability-gated; release smoke required | Not supported until tested |
| Accessibility tree | managed `Accessibility.getFullAXTree`/`getPartialAXTree` plus bounded frame metadata | Observe + Debug plus MCP `full` profile | Capability-gated | Capability-gated; release smoke required | Not supported until tested |
| Reversible tab emulation | managed `Emulation.*` plus compatibility `Network.emulateNetworkConditions` | Observe + Debug plus MCP `full` profile; reset needs Debug only | Capability-gated | Capability-gated; release smoke required | Not supported until tested |
| Personal-data tools beyond sessions/groups | domain-specific optional APIs | Personal data plus MCP `full` profile | Planned | Planned | Not supported until tested |

`browser_get_capabilities` exposes the server's current view. A missing API,
permission, site grant, feature flag, or acceptable browser version removes the
corresponding command. Consumers must handle `CAPABILITY_UNAVAILABLE` and must
not infer support merely from a browser name or version.

## Namespace and Compatibility Layer

The v1 extension uses the promise-based `chrome` namespace directly. Chrome
116 and Edge expose that namespace consistently for the APIs in the supported
matrix, so the production package does not include a WebExtension polyfill.
Using `browser` would not cover the whole supported Chrome range: Chrome only
added that namespace much later. Mozilla also documents meaningful differences
in API coverage, manifests, data cloning, and debugging behavior; see
[Chrome incompatibilities](https://developer.mozilla.org/en-US/docs/Mozilla/Add-ons/WebExtensions/Chrome_incompatibilities).

Domain handlers accept an injected Chrome API object, which is the seam for a
future compatibility adapter. Firefox support must add and test an explicit
adapter rather than aliasing globals. It also requires a Firefox manifest,
permission mapping, service-worker lifecycle strategy, replacement for
`chrome.debugger`/CDP features, protocol capability fixtures, and a real Firefox
E2E job.

## Release Validation

A release candidate must pass:

1. the extension unit suite at the declared minimum capability boundary (115
   fails closed; 116 enables supported APIs);
2. the real two-profile Chrome for Testing E2E on the CI-resolved browser;
3. unpacked installation, pairing, reconnect, Observe permission, page action,
   screenshot, and revoke smoke tests on current Chrome Stable;
4. the same manual smoke test on current Edge Stable;
5. capability comparison against this matrix, with any missing command treated
   as unsupported instead of bypassed.

Best-effort Chromium products may be reported as compatible only after the same
smoke test, but their failure does not block a v1 release. Chrome or Edge
regressions do block release unless the support matrix and product scope are
explicitly revised.
