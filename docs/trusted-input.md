# Trusted CDP Input

Status: implemented for bounded root-document pointer, keyboard, and wheel input

Last reviewed: 2026-08-24

Interaction tools default to `backend: "auto"`, which selects the isolated
content-script implementation and does not require Debug permission. Callers
can explicitly select `backend: "content"` for the same behavior. The following
tools also accept `backend: "cdp"`:

- click, double-click, and context-click;
- hover;
- fill, type, and clear for text-editable controls;
- key and modifier chords;
- set/toggle checkbox or radio state; and
- page or element wheel scrolling with `behavior: "auto"`.

The CDP backend requires Observe site access, the optional Debug permission,
and a root-document target. Focus/blur, select-option, drag/drop, custom-event
dispatch, submit, and smooth scroll remain content-only because their typed DOM
semantics cannot be represented faithfully by the approved low-level input
methods. A trusted fill of a `<select>` is likewise rejected in favor of
`browser_select_option`.

## Execution Boundary

The content script resolves the normal locator, checks document identity,
scrolls the element into view when needed, performs visibility/enabled/pointer
actionability checks, and returns only the element description and a bounded
viewport point. Text supplied by fill/type never passes through the content
preparation message. The extension then acquires a request-scoped managed CDP
lease with the exact minimum subset of:

- `Input.dispatchMouseEvent`;
- `Input.dispatchKeyEvent`; and
- `Input.insertText`.

No raw method name or CDP parameters are caller-controlled. Mouse button and
modifier bit fields are fixed mappings. Clear/fill uses the browser editing
`selectAll` command followed by Backspace; typing with no delay uses one bounded
insert operation, while delayed typing is cancellable between Unicode code
points. The lease is released on success, browser error, cancellation, timeout,
navigation, or debugger detach.

After input, the content script resolves the locator again and returns the
actual element state with `backend: "cdp"`. Password and secret-shaped values
remain redacted. The service worker also rechecks tab window, origin access,
and document identity. When `waitForNavigation` is enabled, the pre-action
element description is retained and the existing frame-scoped navigation
waiter owns the document transition instead of treating it as stale.

## Limits and Failure Behavior

- The existing command deadline (maximum 120 seconds), locator bounds, request
  size, scroll delta, click-count, modifier, and typing-delay limits apply.
  Text is limited to 100,000 Unicode characters; non-zero per-character delay
  is limited to 10,000 characters, and key names to 100 characters.
- CDP input is root-frame only; child-frame coordinates fail closed rather than
  being guessed across process/frame boundaries.
- `behavior: "smooth"` is rejected for trusted wheel input.
- Missing Debug permission returns `PERMISSION_REQUIRED` before attachment.
- A debugger conflict or unavailable managed session returns a stable
  capability/browser error and does not fall back from explicit `cdp` to
  content events.
- `auto` intentionally remains content-first, so granting Debug does not
  silently change page-visible event behavior.

Chrome exposes the Input domain through `chrome.debugger`; the extension uses
that documented transport and only its typed methods. See the official
[`chrome.debugger` API](https://developer.chrome.com/docs/extensions/reference/api/debugger)
and [CDP Input domain](https://chromedevtools.github.io/devtools-protocol/tot/Input/).
