# Performance Diagnostics

Status: implemented

Last reviewed: 2026-08-24

Performance diagnostics are two dedicated MCP `full` tools backed by short
managed CDP sessions. They require the extension's Observe site grant and Debug
permission, work only against the current root HTTP(S) document, and are not
available through batch or `browser_send_command`.

## Tools

`browser_get_performance_metrics` invokes only `Performance.getMetrics`. It
returns at most 200 unique metrics with non-empty names and finite numeric
values. The extension and Go server both validate the tab and document
identity; metrics remain inline because they are small numeric observations.

`browser_capture_performance` accepts one capture kind:

| Kind | Fixed CDP lifecycle | Artifact shape |
| --- | --- | --- |
| `trace` | `Tracing.start` → timed capture → `Tracing.end` → private `IO.read`/`IO.close` | Chrome JSON trace |
| `coverage` | `Profiler.enable` → precise coverage start/take/stop → disable | Coverage result and timestamp |
| `cpuProfile` | `Profiler.enable` → fixed 1,000 µs sampling → start/stop → disable | CPU profile |
| `audits` | `Audits.enable` → bounded `Audits.issueAdded` collection → disable | Up to 500 audit issues |

Capture duration is an integer from 100 to 10,000 milliseconds. The caller may
set an artifact ceiling from 64 KiB to 2,000,000 bytes; the default is the
maximum. An explicit tool timeout must exceed the requested duration by at
least one second so the handler has time to stop and persist the capture.

Trace categories are extension-owned and fixed to user timing, DevTools
timeline, loading, and V8 execution. Coverage always uses detailed precise
coverage with call counts. CPU sampling interval and audit event selection are
also fixed. Callers cannot supply categories, profiler options, stream handles,
CDP methods, or session IDs.

## Artifact and Data Boundary

Captures are encoded as valid JSON objects and independently checked against
the requested kind, byte ceiling, MIME type, tab, document, and duration before
the Go server stores them. The MCP response contains only artifact metadata and
a `browser://artifacts/{artifactId}` URI. It never includes base64 or capture
content.

Trace, coverage, profile, and audit data can contain page URLs, script names,
source locations, and user activity. Reliable field-level redaction would make
the diagnostics incomplete, so capture content is not redacted. Artifacts use
the common unpredictable-ID, owner-only, quota- and TTL-controlled store and
the response always warns that the content is sensitive. Audit logs record
only the capture kind, browser/tab identity, outcome, result size, and duration.

## Lifecycle and Failure Semantics

Each invocation obtains one kind-specific exact-method lease from the shared
CDP Session Manager. It rechecks the root document before and after capture.
The handler stops active tracing, coverage, profiling, or audits in `finally`,
closes its private trace stream, and releases the lease on success, invalid
data, timeout, cancellation, permission loss, detach, or navigation.

The stable failures include `PERMISSION_REQUIRED`, `RESTRICTED_URL`,
`TAB_NOT_FOUND`, `FRAME_NOT_FOUND`, `STALE_TARGET`, `CAPABILITY_UNAVAILABLE`,
`TIMEOUT`, `CANCELLED`, `PAYLOAD_TOO_LARGE`, and `INVALID_MESSAGE` for invalid
parameters or browser results. A prohibited capture kind such as a heap
snapshot returns `INVALID_COMMAND`.

Heap snapshots, `HeapProfiler`, caller-controlled trace categories, raw `IO`
access, returned stream handles, continuous profiler/coverage sessions, and
unbounded event collection are not implemented. Adding any of them requires a
new security review.

The CDP method contracts follow the official DevTools Protocol documentation
for [Performance](https://chromedevtools.github.io/devtools-protocol/tot/Performance/),
[Tracing](https://chromedevtools.github.io/devtools-protocol/tot/Tracing/),
[Profiler](https://chromedevtools.github.io/devtools-protocol/tot/Profiler/),
[Audits](https://chromedevtools.github.io/devtools-protocol/tot/Audits/), and
[IO](https://chromedevtools.github.io/devtools-protocol/tot/IO/).

## Verification

Extension tests cover every capture lifecycle, exact commands and parameters,
trace stream closure, permission/origin/document gates, cancellation cleanup,
prohibited options, and oversized artifacts. Go tests cover multi-browser
routing, root-document binding, wire validation, artifact-only output,
metadata-only audit, tool profiles, batch exclusion, and generic-command bypass
denial. The normal `make verify` gate also runs race tests, lint, generated
reference checks, coverage policy, and the full extension suite.
