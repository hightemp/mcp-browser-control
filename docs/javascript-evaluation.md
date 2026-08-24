# Isolated-World JavaScript Evaluation

Status: implemented with an explicit disabled-by-default feature flag

Last reviewed: 2026-08-24

`browser_evaluate_javascript` evaluates one JavaScript expression in the root
frame of one selected tab. It is available only when all of these gates are
open:

- the MCP server uses the `full` tool profile;
- the target is an allowed HTTP(S) origin with Observe site access;
- the user has granted the optional Debug permission; and
- **Enable isolated-world JavaScript evaluation (advanced)** is checked in the
  extension settings.

The feature flag defaults to off. Saving settings reconnects the extension and
updates its advertised capabilities. Clearing the checkbox removes
`runtime.evaluateIsolated`; removing Debug or Observe access also makes the
command unavailable.

## Execution Boundary

The extension creates or resolves a named isolated world with
`grantUniveralAccess: false`. It assigns that world a restrictive CSP that
denies connections, workers, forms, frames, objects, images, media, and styles;
only the evaluated script itself is permitted. `Runtime.evaluate` also disables
the command-line API, user-gesture treatment, previews, breakpoint pauses, and
unsafe CSP bypass.

The exact managed CDP method allowlist is:

- `Page.getFrameTree`;
- `Page.createIsolatedWorld`;
- `Runtime.evaluate`; and
- `Runtime.releaseObjectGroup`.

Every request uses a unique object group and releases it in `finally`. The
managed CDP lease is then released even after cancellation or failure, so no
remote object handle is returned or retained by the tool. The isolated
execution context itself is owned by Chrome for the current document lifetime;
the tool does not install persistent or navigation-time scripts.

Only the top-level HTTP(S) frame is supported. The extension checks Debug,
origin permission, URL scheme, and root `documentId` before acquiring CDP,
before world creation, before evaluation, and after evaluation. A navigation
causes `STALE_TARGET` instead of allowing the result to be attributed to a new
document.

Main-world execution is not implemented. It is not enabled by this feature
flag, cannot be selected by an argument, and remains prohibited pending a new
security review. The generic command tool cannot reach the internal raw CDP
methods.

## Input and Result Limits

| Boundary | Default | Allowed range |
| --- | ---: | ---: |
| Expression length | caller supplied | 1–32,768 characters |
| Command and V8 execution timeout | 5,000 ms | 100–10,000 ms |
| Result depth | 6 | 0–10 |
| Result values | 1,000 | 1–5,000 |
| Characters per result string | 10,000 | 1–100,000 |
| Complete extension result | 512 KiB | 64 KiB–1,000,000 bytes |
| Object-key length | 256 characters | fixed maximum |

Promise results are awaited by default and can be returned without waiting by
setting `awaitPromise: false`. Successful values are limited to JSON-safe
`null`, booleans, finite numbers, strings, arrays, and plain objects. Depth,
node, string, and key overflow is truncated deterministically and reported by
`truncated` and `warnings`. `undefined`, functions, symbols, browser objects,
DOM nodes, and other non-plain values return a bounded type marker without an
object handle. `NaN`, infinities, negative zero, and BigInt use a fixed
`unserializableValue` string representation.

A JavaScript exception is an executed tool result with `completed: false` and
bounded text, description, line, and column fields. Stack frames, script IDs,
object IDs, previews, and source text are not returned. The extension enforces
the limits before transport; the Go server rejects malformed, mistyped,
over-depth, over-count, or oversized results independently and applies the
normal server redaction boundary.

## Residual Risk

An authenticated MCP caller with every gate enabled can read sensitive DOM
content and mutate the shared DOM. An expression may trigger page behavior or
navigation through DOM operations even though main-world JavaScript state and
network-producing APIs in the isolated world are constrained. Cancellation
or a post-execution stale-document error cannot prove that such side effects
did not occur. Use this tool only for trusted MCP clients and narrow origin
allowlists.

Expressions and returned values are excluded from audit metadata, but returned
DOM data is still sensitive. Server redaction is defense in depth and cannot
guarantee removal of every application-specific secret. The tool is excluded
from `browser_batch` to keep each invocation and deadline explicit, and
`browser_send_command` rejects this capability so it cannot bypass the typed
server validation.

## Verification Boundary

Automated tests cover opt-in capability advertisement, full-profile and batch
gates, exact CDP methods and safe parameters, object-group release, JSON type
normalization, depth/node/string/byte truncation, exceptions, missing Debug or
site permission, stale documents, malformed extension results, and server
redaction. Release validation still requires current Chrome and Edge smoke
tests for CSP behavior, promises, navigation races, DevTools attachment
conflicts, cancellation, permission revocation, and repeated evaluation in one
document.

Method and parameter definitions come from the official Chrome DevTools
Protocol [Page](https://chromedevtools.github.io/devtools-protocol/tot/Page/)
and [Runtime](https://chromedevtools.github.io/devtools-protocol/tot/Runtime/)
domains.
