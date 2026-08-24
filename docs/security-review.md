# Security Review for Sensitive and Expert Browser Features

Status: approved with mandatory gates

Reviewed baseline: protocol v1, server v0.3.0

Scope: planned P1/P2 debugging, personal-data, CDP, performance, clipboard,
file-input, proxy, content-settings, and browsing-data features

## Decision Summary

The local-only product may add the conditional features listed below only
through dedicated typed tools and the mandatory controls in this review. Raw
CDP, personal data, request bodies, evaluation, and artifacts are never part of
the `standard` tool profile. New tools and CDP methods fail closed until they
are explicitly classified and tested.

The following capabilities remain prohibited in production: unrestricted CDP,
main-world evaluation by default, arbitrary local file paths, permission-prompt
automation, credential or password-store access, extension management,
unbounded personal-data export, and silent proxy/content-setting changes.

## Security Model

The server and extension protect these assets:

| Asset | Sensitivity | Required protection |
| --- | --- | --- |
| MCP bearer token and browser credential | Critical | Owner-only storage, no logs/results, revocation, replay resistance |
| Authenticated page state and DOM | High | Origin policy, explicit site permission, bounded/redacted output |
| Cookies, storage, history, bookmarks, clipboard | High | Personal-data profile, explicit sensitive mode, pagination, audit |
| Browser control authority | High | Loopback boundary, authentication, tool/CDP allowlists, confirmation |
| Screenshots, traces, bodies, generated PDFs | High | Owner-only artifacts, quota, TTL, unpredictable IDs, authenticated reads |
| Availability of browser/server | Medium | Deadlines, payload/rate limits, bounded queues and capture buffers |

The trust boundaries are:

1. an untrusted website and the isolated content script;
2. the content script/main-world bridge and extension service worker;
3. the authenticated extension WebSocket and local server;
4. an authenticated MCP client and server tools;
5. the server and owner-only credential/artifact files;
6. the extension and Chrome's optional permission/debugger prompts.

The supported deployment assumes a single local OS owner. A process already
able to read that owner's browser profile, token file, process memory, or
artifact directory is outside the containment boundary. Remote listeners and
multi-user shared-host operation are not approved by this review.

### Reviewed Control Evidence

| Control | Implementation evidence |
| --- | --- |
| Local network and bearer boundary | `internal/netguard/http.go`, `internal/transport/websocket/server.go` |
| Pairing, hashing, expiry, replay prevention, revocation | `internal/security/pairing/manager.go`, `internal/security/token/store.go` |
| Tool/origin/incognito/confirmation policy | `internal/tools/policy.go`, `internal/policy/action.go`, `internal/tools/windows.go` |
| Request correlation, deadlines, disconnect cleanup | `internal/router`, `internal/transport/websocket` |
| Result redaction and traversal/output limits | `internal/redaction`, `internal/tools/tools.go` |
| Owner-only artifact TTL/quota boundary | `internal/artifacts/store.go` |
| Extension command and content-bridge validation | `chrome-extension/src/command-router.js`, `chrome-extension/src/content-bridge.js`, `chrome-extension/src/content.js` |
| Explicit optional permission UI | `chrome-extension/src/options.js`, `chrome-extension/src/permission-profiles.js` |

## Threat Register

| ID | Threat and abuse case | Existing controls | Residual risk and required P1/P2 gate |
| --- | --- | --- | --- |
| SR-01 | A malicious page forges bridge messages, returns adversarial DOM, or tries to make the extension execute commands. | Pages cannot reach extension runtime listeners directly; runtime messages check extension sender identity; bridge versions, command allowlists, target/document identity, actionability, output bounds, and redaction are enforced. Main-world console events are treated as untrusted data. | A page can forge or poison console observations because same-window `postMessage` is not an authenticity boundary. Console data must never become executable input or policy evidence. Fuzz bridge envelopes and preserve per-document limits. |
| SR-02 | A malicious local website attempts DNS rebinding or direct MCP/WebSocket control. | Both listeners require loopback `Host`; browser origins are checked; MCP HTTP requires a random bearer token; extension registration requires pairing/credential authentication; request and message rate limits apply. | A loopback website may reach the socket but cannot authenticate. Deployment-specific exact client-origin allowlists are recommended. Remote mode requires a new review and is forbidden now. |
| SR-03 | A compromised or over-privileged MCP client uses legitimate tools to exfiltrate data or damage a browser session. | Owner bearer token, fail-closed tool profiles, page origin allow/deny policy, incognito opt-in, live capability/permission checks, and `confirm: true` for destructive multi-item actions. | An authenticated client is intentionally powerful within its configured policy. Sensitive features require `full`, explicit browser grants, narrow origins, audit, and dedicated tools. A confirmation flag is intent evidence, not human authentication. |
| SR-04 | A stolen browser credential or pairing code is replayed. | Pairing codes are random, rate-limited, expire, and rotate after one use. Browser credentials are random, browser-ID-bound, revocable, stored raw only in extension storage, and persisted server-side only as hashes in owner-only files. | A stolen long-lived extension credential and browser ID can be replayed locally until revocation. Credential rotation is mandatory before any future remote mode; local MVP accepts this residual risk. Authentication failures must not reveal credential validity details. |
| SR-05 | Secrets escape through logs, errors, artifacts, screenshots, traces, or response bodies. | Extension redaction plus an independent server redaction boundary, safe errors, output limits, owner-only artifact files, random artifact IDs, TTL, quota, and authenticated resource access. Audit records omit arguments/results and full URLs. | Pixels, PDFs, traces, network bodies, and some personal-data exports are inherently sensitive and cannot be reliably redacted. They require an explicit sensitive-content warning and artifact storage; never inline them in normal logs/results. |
| SR-06 | Raw CDP or JavaScript evaluation bypasses tool, origin, permission, or data controls. | Raw CDP is absent from the extension command allowlist; `browser_send_command` cannot bypass it and is hidden outside `full`. Debugger permission is optional and user-granted. | Every future CDP consumer must use one ref-counted session manager and a versioned method allowlist. Typed tools must reapply origin, permission, deadline, redaction, result-size, and audit checks. Main-world and persistent evaluation remain prohibited by default. |
| SR-07 | History, bookmarks, cookies, clipboard, downloads, or storage reveal personal data or permit destructive changes. | Personal-data permissions are optional and only requested by a settings-page click. Current capability checks fail before unavailable commands. Server redaction masks secret-shaped values and local paths. | Each domain requires separate read/mutate tools, pagination, exact scopes, field-level redaction, and audit. Bulk mutation requires confirmation. Clipboard access must use the one-shot popup flow in the T-074 design; unattended read and arbitrary file paths remain prohibited. Automated file input may accept only server-owned artifacts through a dedicated typed CDP command. |
| SR-08 | Oversized commands/results, event floods, capture buffers, CDP streams, or artifacts exhaust memory, disk, goroutines, or browser resources. | HTTP/WebSocket size limits, per-session/per-connection token buckets, bounded send queues, command deadlines/cancellation, result traversal/output budgets, bounded console buffers, artifact quota/TTL, and backpressure errors. | CDP event domains, HAR, tracing, coverage, and batch need independent item/byte/time/concurrency limits and deterministic detach/cleanup. Soak and forced-disconnect tests are required before enablement. |

## Approved Conditional Features

Approval here means implementation may begin; it does not bypass the gates in
the next section.

| Feature | Approved behavior | Required profile and restrictions |
| --- | --- | --- |
| Print to PDF, accessibility, emulation | Dedicated typed tools with target/origin checks and bounded results/artifacts | `standard` for page-bridge accessibility; `full` plus Debug for PDF and CDP-only controls |
| JavaScript evaluation | Ephemeral isolated-world expression, fixed timeout, JSON-safe bounded result, no retained handles | `full` plus Debug and explicit feature flag; main world requires a later review |
| Network diagnostics | Metadata first; allowlisted response bodies only with MIME/size/origin filters and redaction | `full` plus Observe/Debug; no interception or modification |
| Cookies and origin storage | Exact-origin list/get/set/remove; masked values by default; bounded metadata | `full` plus Personal data and Observe; unmasked values require explicit sensitive-data mode |
| Downloads | Create and lifecycle/status operations; redact paths; never read downloaded file contents | `full` plus Personal data; erase-history batches require confirmation |
| History, bookmarks, reading list | Paginated scoped reads and single-item mutations; separately named read/mutate tools | `full` plus Personal data; bulk deletion requires confirmation and audit |
| Performance metrics and tracing | Bounded sessions, safe category allowlist, automatic stop/detach, artifact output | `full` plus Debug; no heap snapshots or unbounded profiler/coverage streams |
| Browsing-data clearing | Exact data-type/time/origin scope with dry-run summary | `full` plus Personal data, explicit product opt-in, and `confirm: true` |
| Clipboard write | One-shot bounded plain text supplied by the MCP caller and copied directly from an explicit extension-popup click | `full` plus Personal data; 30-second pending request, no persistence or batch |
| Clipboard read | One-shot bounded plain text read directly from an explicit extension-popup click | Not approved until focused implementation review; requires `full`, Personal data, sensitive-data mode, and visible disclosure to the user |
| File input | Native user selection, or a dedicated typed CDP tool using only MCP-supplied server-owned artifacts | `full`; automated mode also requires Debug, exact origin, confirmation, and no path parameter |

### Raw CDP Boundary

Raw CDP remains disabled by default and is allowed only in `full` with Debug
permission. Its first version may expose a reviewed subset of these read-only
methods: `Accessibility.getFullAXTree`, `Accessibility.getPartialAXTree`,
`Accessibility.queryAXTree`, `DOM.describeNode`, `DOM.getBoxModel`,
`Page.getLayoutMetrics`, and `Performance.getMetrics`. Trace start/stop and
stream reads must stay behind a dedicated performance tool so the server owns
the session and stream handle.

Adding any other method is a security change requiring an entry in the method
allowlist, parameter/result schema, data classification, resource limits,
audit event, negative test, and update to this review.

## Prohibited Functions

The following are not approved for a raw tool or standard production build:

- `Browser.close`, permission grant/reset, extension management, and arbitrary
  download-path configuration;
- unrestricted `Target.*`, `Security.*`, `Storage.*`, `SystemInfo.*`,
  `HeapProfiler.*`, or arbitrary `IO.read` access;
- `Runtime.evaluate`, `Runtime.callFunctionOn`, compiled/persistent scripts, or
  `Page.addScriptToEvaluateOnNewDocument` through raw CDP;
- `Fetch.*`, request interception/modification, authentication challenge
  handling, arbitrary headers, or traffic redirection;
- raw cookie commands, cache/cookie clearing, and unscoped storage deletion;
- `DOM.setFileInputFiles` with local paths and any filesystem discovery/read;
- unattended clipboard read, clipboard watching, or any attempt to synthesize
  or bypass the reviewed popup user-gesture flow;
- password manager, autofill, payment, browser-sync, or credential-store data;
- silent proxy, privacy, content-setting, certificate, or geolocation-policy
  changes;
- unbounded history/bookmark/cookie/storage export, heap snapshots, network
  bodies, traces, profiler data, coverage, batch results, or event streams.

Typed implementations may use an otherwise prohibited CDP primitive internally
only after a focused review proves that callers cannot control the raw method,
target outside policy, unsafe parameters, or stream handles.

## Mandatory Feature Gates

Every sensitive or expert feature must satisfy all applicable gates before its
task can be marked complete:

1. A dedicated typed tool and protocol command exist; new names fail closed in
   the tool and extension command allowlists.
2. The tool is absent from `minimal` and `standard` unless this document
   explicitly says otherwise.
3. Required optional permissions are requested only by a user click in the
   extension settings UI, and live capability checks fail before browser APIs.
4. Browser ID, tab/frame/document target, origin policy, restricted URL, and
   incognito policy are checked before the sensitive operation.
5. Input has a strict schema, item/byte/depth/count limits, one deadline, and
   cancellation with deterministic cleanup/detach.
6. Output is redacted and bounded. Inherently sensitive binary/stream output is
   stored as an owner-only, quota/TTL-controlled artifact with a warning.
7. Mutating and read operations are separate. Destructive multi-item actions
   require `confirm: true`; a dry-run is preferred.
8. Audit logs record only safe metadata: operation, browser/target IDs, origin,
   outcome, rule, counts, and duration. They never record values, query strings,
   bodies, expressions, clipboard text, or local paths.
9. Unit, contract, negative security, race, disconnect/cleanup, payload-bomb,
   flood, and multi-browser isolation tests pass.
10. Documentation names the permission warning, data sensitivity, residual
    risk, and disable/revoke procedure.

## Review Outcome by Planned Task

| Task | Outcome |
| --- | --- |
| T-060 CDP Session Manager | Approved as prerequisite; one session per target, reference counts, bounded consumers, forced detach |
| T-062 network capture | Conditional approval for metadata and bounded allowlisted bodies; interception remains prohibited |
| T-063 emulation | Conditional approval through typed reversible settings with restore-on-detach |
| T-064 accessibility | Approved with tree/node bounds and origin checks |
| T-065 evaluation | Conditional isolated-world-only approval; raw/main-world/persistent execution prohibited |
| T-066 raw CDP | Conditional approval for the initial read-only method list above; disabled by default |
| T-067 performance | Conditional approval for bounded metrics/traces in artifacts; heap snapshots prohibited |
| T-070–T-073 personal data | Conditional approval with Personal data profile, pagination/redaction, separated mutation, and confirmation |
| T-074 clipboard/file input | Security design completed in [`clipboard-file-input-design.md`](clipboard-file-input-design.md); one-shot popup clipboard write and artifact-only typed file input are conditional, clipboard read needs a focused implementation review, and arbitrary paths remain prohibited |
| T-075 proxy/content settings/browsing data | Browsing-data clear is conditional; proxy/content settings remain prohibited pending a new product/security decision |

This review must be reopened if the server gains remote binding, supports
multiple OS users, accepts third-party extensions, changes credential storage,
or adds a sensitive feature not classified above.
