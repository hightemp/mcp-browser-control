# Clipboard and File Input Security Design

Status: research complete; implementation requires the gates below

Last reviewed: 2026-08-24

This document defines the only approved design directions for future clipboard
and file-input tools. It does not add a runtime capability. Unattended
clipboard access, arbitrary local paths, filesystem discovery, and simulated
permission or user-activation bypasses remain prohibited.

## Platform Findings

Chrome exposes `clipboardRead` and `clipboardWrite` as extension permissions
with explicit warnings that the extension can read or modify copied data. They
are already declared as optional Personal data permissions in this project and
must continue to be requested only from the extension settings UI. Chrome's
[permission reference](https://developer.chrome.com/docs/extensions/reference/permissions-list)
documents both warnings, while the
[`chrome.permissions` guide](https://developer.chrome.com/docs/extensions/reference/api/permissions)
requires an optional permission request to originate in a user gesture.

An MV3 service worker has no DOM. Chrome 109 introduced `chrome.offscreen` for
hidden DOM documents and includes a `CLIPBOARD` reason, but an offscreen
document is not evidence of user intent and cannot be used to manufacture or
bypass a trusted gesture. The project minimum of Chrome 116 is new enough for
the API, and Edge lists `offscreen` and `permissions` as supported MV3 APIs.
See the Chrome
[`offscreen` reference](https://developer.chrome.com/docs/extensions/reference/api/offscreen)
and the Microsoft Edge
[extension API matrix](https://learn.microsoft.com/en-us/microsoft-edge/extensions/developer-guide/api-support).

Clipboard contents are ambient OS data, commonly including passwords and
personal information. Browser behavior for gesture and permission checks has
also varied. The
[Async Clipboard security guidance](https://web.dev/articles/async-clipboard#security_and_permissions)
therefore supports a product rule stronger than the minimum browser behavior:
an authenticated MCP request is never treated as a user gesture.

For file input, the HTML picker consumes transient user activation and asks the
user to make a selection. The
[HTML Standard picker algorithm](https://html.spec.whatwg.org/multipage/input.html#dom-input-showpicker)
is the normative basis for the manual flow. File System Access pickers also
require a secure context and a user gesture, as documented in Chrome's
[File System Access guide](https://developer.chrome.com/docs/capabilities/web-apis/file-system-access).

CDP can avoid the native chooser, but
[`DOM.setFileInputFiles`](https://chromedevtools.github.io/devtools-protocol/tot/DOM/#method-setFileInputFiles)
accepts an array of local filesystem paths. The related experimental
[`Page.setInterceptFileChooserDialog`](https://chromedevtools.github.io/devtools-protocol/tot/Page/#method-setInterceptFileChooserDialog)
suppresses the native dialog and transfers control to a protocol client. These
primitives are suitable only behind a typed artifact-based tool; exposing their
path parameter to MCP would create arbitrary local-file disclosure.

## Security Decisions

### Clipboard

No background or batch-capable clipboard tool is approved. A future
implementation may use a short-lived, one-shot consent flow:

1. A `full`-profile MCP call creates a pending request for one browser profile.
2. The extension popup shows the operation, data type, byte count, requesting
   MCP client label, and a 30-second expiry without showing clipboard contents.
3. The popup fetches the bounded value into popup memory before confirmation.
4. The user clicks **Copy now** or **Paste once**. The popup invokes the
   Clipboard API directly from that click handler; no synthetic event,
   offscreen document, or delayed background callback substitutes for the
   gesture.
5. The pending request is consumed exactly once and erased from server,
   service-worker, and popup memory on success, denial, popup close, disconnect,
   or expiry.

The first implementable scope is plain-text write only. It must enforce:

- Personal data permission plus MCP `full` profile;
- an explicit extension feature toggle and a live `clipboardWrite` grant;
- a maximum of 64 KiB of valid UTF-8 text;
- one pending request per browser and no `browser_batch` eligibility;
- no persistence in `chrome.storage`, server files, artifacts, history, or
  retry queues;
- no clipboard content in logs, errors, notifications, diagnostics, or audit;
- an audit record containing only operation, browser ID, byte count, outcome,
  and duration;
- a visible success or denial state in the popup.

HTML, images, custom MIME formats, clipboard watching, and read-after-write
verification are not approved for the first implementation.

Clipboard read remains more sensitive than write. A future `Paste once` tool
must use the same popup gesture and additionally require an explicit
sensitive-data mode. The user must see that clipboard contents will be returned
to the named MCP client. The result is limited to 64 KiB of text, passes through
the server redaction boundary, and is never retained. Unattended read, polling,
change events, history, fallback to `document.execCommand`, and retry after a
lost gesture are prohibited. Implementation requires a focused review and real
Chrome and Edge tests before the capability may be advertised.

### File Input

Two distinct modes are allowed by design.

The manual mode leaves the native chooser under user control. The tool may
resolve and highlight one strict `input[type=file]`, then report that user
selection is required. The user clicks the page control and chooses files in
the native dialog. The extension may observe only bounded post-selection
metadata already visible to the page: count, base filenames, sizes, and MIME
types. It never receives full paths or file contents from the chooser. This
mode must not claim completion until the element emits a real selection event.

The automated mode is conditional on the CDP Session Manager and a new
authenticated input-artifact ingress. Its typed interface accepts only:

```json
{
  "browserId": "browser-id",
  "tabId": 42,
  "locator": { "label": "Upload receipt" },
  "artifactUris": ["browser://artifacts/artifact-id"],
  "confirm": true
}
```

There is no `path`, directory, URL, glob, or raw byte parameter. Each artifact
must have been supplied by the MCP client to the server-owned artifact store,
not discovered from the host. The server resolves its canonical owner-only
path internally and passes it only through the dedicated extension command to
`DOM.setFileInputFiles`. That command must be denied by
`browser_send_command`, raw CDP, batch, and every extension command path that
does not carry the server's validated typed request.

The initial automated implementation must enforce:

- MCP `full`, Debug permission, Observe access to the exact target origin, an
  explicit file-input feature toggle, and `confirm: true`;
- strict locator resolution to a mutable `input[type=file]` in the current
  document, with browser/frame/document identity checked again immediately
  before CDP dispatch;
- at most 5 files, 10 MiB total, and 5 MiB per file, with smaller configurable
  limits allowed;
- safe base filenames, declared MIME metadata, and compatibility checks against
  the element's `accept` and `multiple` attributes; these checks are policy
  gates, not proof that content is safe;
- regular files created by the artifact store itself, with no symlinks,
  devices, FIFOs, sockets, path traversal, hard-link substitution, or
  caller-controlled filesystem names;
- one deadline and deterministic CDP attach/detach through T-060;
- deletion at the earliest safe point after selection is cleared, target
  navigation/detach, explicit cancellation, or a short TTL;
- result metadata only; never echo paths or file contents;
- audit metadata limited to browser/target, origin, file count, total bytes,
  coarse MIME categories, outcome, and duration.

Files obtained from screenshots, PDFs, downloads, or page/network capture are
not automatically eligible as input artifacts. Reuse requires an explicit
client action and the same confirmation because it changes the data-flow
purpose. Directory upload and relative-path trees are out of scope.

Assigning synthetic `File` objects through page JavaScript is not the approved
fallback. It is inconsistent with native selection, exposes bytes to page
execution contexts, can be distinguished by sites, and encourages main-world
evaluation. Native user selection is the fallback whenever the typed CDP path
is unavailable.

## Browser Matrix

The product-level support policy in
[`browser-support.md`](browser-support.md) remains authoritative.

| Capability | Chrome 116+ desktop | Edge 116+ desktop | Other Chromium 116+ | Firefox | Mobile |
| --- | --- | --- | --- | --- | --- |
| One-shot clipboard text write | Conditional: popup gesture, optional `clipboardWrite` | Conditional: same flow; release smoke required | Best effort after capability and gesture tests | Not supported in v1 | Not supported |
| One-shot clipboard text read | Not approved until focused implementation review and E2E | Same | Not supported | Not supported | Not supported |
| Native user file selection | Supported as a user-owned browser flow; MCP cannot choose the file | Supported as a user-owned browser flow | Best effort | Product unsupported | Product unsupported |
| Artifact-backed file input | Planned after T-060 and input-artifact ingress; Debug + `full` | Planned; Edge debugger/CDP smoke required | Not supported until tested | Not supported | Not supported |
| Arbitrary local path input | Prohibited | Prohibited | Prohibited | Prohibited | Prohibited |

Runtime capability negotiation is mandatory even for a browser listed as
supported. Clipboard capabilities must be absent unless the feature toggle,
exact optional permission, required DOM context, and UI flow are all available.
Artifact-backed file input must be absent unless the CDP session manager,
dedicated command, artifact ingress, and every validation gate are available.

## Threats and Required Tests

| Threat | Required control and negative test |
| --- | --- |
| MCP client reads ambient secrets | No unattended read; verify direct, batch, reconnect, and raw-command attempts cannot advertise or invoke it |
| Page or MCP client fakes user activation | Perform the Clipboard API call in the popup click handler; test synthetic events, expired requests, popup close, and delayed callbacks |
| Clipboard data leaks through state | Inspect logs, errors, diagnostics, storage, crash paths, and reconnect queues with secret-bearing fixtures |
| Caller supplies `/etc/passwd`, a home path, URL, glob, or traversal | Schema omits paths; reject unknown fields and test every raw/batch/CDP bypass |
| Artifact changes after validation | Use immutable server-created files and descriptor-safe validation; test symlink, hard-link, replacement, and expiry races |
| File targets the wrong browser, tab, frame, or document | Bind request and artifact lease to the exact target; test navigation, reconnect, stale document, and two-browser concurrency |
| Page exfiltrates a file unexpectedly | Require exact origin policy, strict file-input locator, confirmation, and purpose-changing artifact reuse consent |
| Oversized input exhausts memory or disk | Enforce streaming ingress, per-file/total/quota limits, deadlines, cleanup, and payload-bomb tests |

The implementation review cannot be closed by unit tests alone. It requires
real current Chrome and Edge tests for gesture preservation, permission denial
and revocation, popup closure, picker cancellation, CDP conflict/detach,
navigation races, artifact cleanup, and multi-browser isolation.

## Reopen Conditions

Reopen this design before adding non-text clipboard formats, clipboard
watching, background clipboard access, remote browser hosts, multi-user server
operation, direct MCP paths, directory upload, native messaging, a different
artifact store, or a browser family outside the current Chromium desktop scope.
