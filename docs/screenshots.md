# Screenshots

Status: implemented

Last reviewed: 2026-08-24

`browser_screenshot` captures one explicitly selected root tab as PNG or JPEG
and returns a temporary artifact URI. It supports three capture modes:

| `capture` | Browser mechanism | Additional requirement |
| --- | --- | --- |
| `viewport` | `tabs.captureVisibleTab` | Observe site access and temporary `activeTab` access from invoking the extension action on the target tab |
| `fullPage` | managed `Page.getLayoutMetrics` and `Page.captureScreenshot` | Debug permission |
| `element` | root-document locator plus the same managed CDP methods | Debug permission |

JPEG quality is an integer from 0 through 100. `locator` is required only for
`element`; all normal locator strategies and document-scoped element
references are accepted. Full-page and element capture are root-document only.
Supplying `documentId` is recommended when a workflow already has one because
the extension rejects a navigation or stale element reference instead of
capturing the replacement document.

## Target and State Guarantees

Viewport capture serializes operations per window. If the selected tab is not
active, the extension activates it, captures it, and restores the previously
active tab in `finally`. Chrome requires either `<all_urls>` or temporary
`activeTab` access for `tabs.captureVisibleTab`. This extension deliberately
uses the narrower `activeTab` permission: open the target tab and click the
extension action once before capture. The grant remains while the tab stays on
the same origin and is revoked when the tab closes or navigates to another
origin.

Full-page and element modes do not scroll, resize, emulate, or activate the
tab. They use CDP `captureBeyondViewport` with a page-coordinate clip. The
extension reads root document identity and layout before capture, rechecks the
tab, origin, window, and document after capture, and reports a warning if the
page itself changed its scroll position or viewport dimensions during the
operation. Element bounds are resolved in the isolated content script and
translated from layout-viewport coordinates immediately before capture.

## Bounds and Artifact Handling

The default and absolute maximums are 16,384 pixels per dimension and
2,000,000 encoded bytes. Callers may request stricter `maxWidth`, `maxHeight`,
and `maxBytes` values. CDP modes reject an oversized CSS clip before image
generation; every mode then validates the returned PNG/JPEG signature, actual
dimensions, base64 length, MIME type, and encoded size in the extension and
again in the Go server.

Inline image bytes are removed from the MCP result. The server stores them in
the owner-only artifact store and returns `browser://artifacts/{artifactId}`
plus its metadata URI and expiry. Screenshot pixels are intentionally not
redacted and can contain credentials, private messages, or other authenticated
page content. The result therefore always includes a sensitive-content
warning; artifact TTL and quota should be kept as small as the workflow allows.

## Failure Behavior

- Missing site access returns `PERMISSION_REQUIRED` before capture.
- Missing temporary active-tab access returns `PERMISSION_REQUIRED` with an
  instruction to invoke the extension action on the target tab; no permission
  prompt is opened by an MCP command.
- Missing Debug permission affects only `fullPage` and `element`; viewport
  capture remains available after the explicit active-tab gesture.
- A missing or ambiguous element uses the normal locator errors.
- Navigation or document replacement returns `STALE_TARGET`.
- A moved/closed tab, debugger conflict, cancellation, timeout, invalid browser
  payload, or exceeded limit fails without returning partial image data.
- Opening DevTools can detach the managed CDP session; retry after closing the
  conflicting debugger and reselecting the current document.
