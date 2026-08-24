# Proxy, Content Settings, and Browsing Data Design Review

Status: design approved; no runtime capability enabled

Last reviewed: 2026-08-24

This review defines whether browser-wide configuration and data-removal APIs
may be exposed through MCP Browser Control. It does not add a tool, extension
command, capability, or product opt-in.

## Decision

| Area | Standard distribution | Future decision |
| --- | --- | --- |
| Proxy | Prohibited | Requires a new product decision, a separate extension flavor and ID, and a focused security review |
| Content settings | Prohibited | Requires a new product decision and focused review, even for read-only access |
| Browsing-data removal | Disabled and unavailable | May be implemented only through the typed, origin-scoped, two-phase design below after explicit product-owner approval |

None of these APIs may be reached through `browser_send_command`,
`browser_batch`, raw CDP, JavaScript evaluation, or a generic extension-command
wrapper. An installed browser permission is never sufficient evidence that the
corresponding MCP operation is approved.

The extension currently declares `browsingData` as optional and includes it in
the aggregate Personal data permission request, but has no browsing-data
handler or advertised capability. Before a cleanup implementation is enabled,
that permission must move to a dedicated **Browsing-data cleanup** consent row.
If the product owner rejects the feature, the unused optional permission must
be removed from the release manifest and permission profile.

## Platform Findings

### Proxy

The [`chrome.proxy` API](https://developer.chrome.com/docs/extensions/reference/api/proxy)
controls browser-wide proxy configuration. It supports direct, system,
auto-detect, PAC-script, and fixed-server modes. A PAC script or proxy endpoint
can observe, redirect, block, or selectively route traffic across unrelated
sites and authenticated sessions.

Chrome does not allow the `proxy` permission to be optional, as documented in
the [optional-permissions limitations](https://developer.chrome.com/docs/extensions/reference/api/permissions).
Installing an extension with that permission produces the high-severity
warning described in Chrome's
[permission warning list](https://developer.chrome.com/docs/extensions/reference/permissions-list).
Consequently, merely adding the permission changes the trust and installation
boundary for every user of that extension package.

The standard manifest must not contain `proxy`, and the protocol must not
advertise proxy configuration or proxy-authentication commands. A future proxy
proposal must use a separately packaged extension with a distinct ID so that
the elevated installation warning and authority cannot silently arrive in an
ordinary update. It must also specify recovery after service-worker crash,
browser restart, server disconnect, or invalid PAC configuration. This review
does not approve such a proposal.

### Content settings

The [`chrome.contentSettings` API](https://developer.chrome.com/docs/extensions/reference/api/contentSettings)
can read, set, and clear site policy for cookies, JavaScript, images, popups,
clipboard access, notifications, camera, microphone, location, and other
categories. Rules may contain primary and secondary match patterns, wildcard
hosts, regular/incognito scope, and browser-managed precedence. A small schema
or precedence mistake can therefore change unrelated websites, suppress a
security prompt, or weaken a managed browser policy.

The standard manifest must not contain `contentSettings`. Both reads and
mutations remain unapproved: read results disclose user or administrator
policy, while mutation is browser-wide persistent authority. Exact-origin
diagnostics, wildcard mutation, generic `set`/`clear`, and permission-prompt
automation are all absent from the protocol. A future read-only proposal still
requires a new product decision, a minimized result schema, managed-policy
handling, and Chrome/Edge tests. Mutation requires its own later review.

### Browsing data

The [`chrome.browsingData` API](https://developer.chrome.com/docs/extensions/reference/api/browsingData)
deletes data from a local browser profile. Chrome supports `origins` and
`excludeOrigins` from version 74 and promise completion from version 96, so the
origin-scoped primitive exists throughout this project's Chrome 116+ range.
The same documentation establishes several important boundaries:

- omitting `since` means all time;
- origin filters apply only to cookies, cache, and site-storage categories;
- clearing cookies for one origin clears cookies for its whole registrable
  domain;
- `origins` and `excludeOrigins` cannot be combined;
- protected-web and extension origins are separate destructive scopes;
- a removal can take tens of seconds, and completion must be awaited; and
- browser Sync may recreate its account cookie after a general removal.

The API does not expose a reliable per-origin item-count preview. A dry run can
therefore describe the normalized deletion plan and its widening effects, but
must never claim how many records or bytes will be deleted.

Microsoft lists `browsingData`, `contentSettings`, and desktop `proxy` support
for Manifest V3 in its
[Edge extension API matrix](https://learn.microsoft.com/en-us/microsoft-edge/extensions/developer-guide/api-support).
That compatibility statement does not replace the Edge release smoke tests or
the product restrictions in this review.

## Approved Future Browsing-Data Contract

Approval in this section permits later implementation work only. The feature
must remain absent until the product owner explicitly enables its build and
configuration gate.

### Tools and scopes

The first implementation must expose exactly two typed MCP tools:

1. `browser_prepare_browsing_data_clear` validates and stores an immutable,
   in-memory deletion plan. It returns a random one-shot `planId`, an expiry no
   more than 60 seconds away, normalized scopes, and human-readable warnings.
2. `browser_clear_browsing_data` accepts only `browserId`, `planId`, and
   `confirm: true`. It consumes the plan once, revalidates every gate, calls
   the typed extension command, and waits for browser completion.

The prepare input is limited to:

| Field | Rule |
| --- | --- |
| `browserId` | Required or resolved once from the MCP session, then bound into the plan |
| `origins` | 1–20 unique, normalized, exact HTTP(S) origins; no paths, credentials, wildcards, opaque origins, extension URLs, or `excludeOrigins` |
| `dataTypes` | 1–8 unique values from the initial allowlist below |
| `since` | Required RFC 3339 timestamp; it cannot be in the future |
| `allTime` | Explicit boolean; required instead of `since` for an all-time plan and called out prominently in the preview |

`since` and `allTime` are mutually exclusive. Time filtering has
browser-defined granularity for site-storage backends and must not be described
as record-level precision.

The initial data-type allowlist is:

- `cache`;
- `cacheStorage`;
- `cookies`;
- `fileSystems`;
- `indexedDB`;
- `localStorage`; and
- `serviceWorkers`.

Cookie plans must warn that the effective scope expands to each origin's
registrable domain. The preview must list those derived registrable domains
before a plan can be confirmed. The implementation must use a maintained
public-suffix parser; it must not derive them by splitting on dots.

`appcache`, `downloads`, `formData`, `history`, `passwords`, `pluginData`,
`serverBoundCertificates`, and `webSQL` are prohibited. Downloads and history
belong to their dedicated typed domains. Autofill, passwords, deprecated
stores, protected-web data, and extension data have no approved MCP deletion
path.

### Mandatory gates

Both tools and the extension command require all of the following:

- MCP `full` profile;
- `features.browsing_data_cleanup = true`, defaulting to `false` and omitted
  from standard release configuration;
- a live, connected, explicitly selected browser;
- the dedicated Browsing-data cleanup permission granted by a user click in
  extension UI, plus a live `chrome.permissions.contains` check;
- every origin allowed by the configured origin policy;
- non-incognito browser context and `originTypes.unprotectedWeb` only;
- an unexpired plan whose browser, normalized origins, types, and time scope
  have not changed;
- `confirm: true`; and
- a single operation deadline up to 120 seconds, with late results discarded.

The command is excluded from batch and raw dispatch. Only one cleanup may run
per browser at a time. Disconnect, timeout, or cancellation makes the result
indeterminate because browser deletion is not transactional and cannot be
rolled back. The error must say that the caller should inspect the affected
sites before retrying; it must not automatically repeat the operation.

### Result and audit

A successful result contains only the browser ID, plan ID, normalized data
types, origin count, registrable-domain count, time scope, completion status,
and duration. It must not contain cookies, stored values, browsing records,
query strings, or a fabricated deletion count.

Audit records contain operation name, browser ID, plan digest, counts, data
types, time scope, policy decision, result, and duration. Raw origins are not
written to the general audit log. Normal application logs never contain the
plan ID or input origin list. Metrics use fixed data-type labels and never use
an origin as a label.

The extension must await the API promise and return only a fixed result shape.
It must not accept arbitrary `RemovalOptions` or `DataTypeSet` objects from the
server.

## Threat Analysis

| Threat | Required mitigation | Residual risk |
| --- | --- | --- |
| A compromised MCP client erases a whole profile | Exact non-empty origins, fixed data types, full profile, feature flag, two-phase one-shot confirmation | An authorized client can still erase approved site data irreversibly |
| A wildcard or malformed URL widens the deletion | Parse and canonicalize on server and extension; accept only exact HTTP(S) origins; reapply origin policy | Cookie scope still expands to the registrable domain by browser design |
| A prepared plan is swapped or replayed | Cryptographically random one-shot ID bound to browser, owner session, normalized digest, and 60-second expiry | A compromised authorized client can prepare and confirm its own plan |
| Batch, raw CDP, or evaluation bypasses confirmation | Dedicated command allowlists; explicit exclusion from batch/raw/evaluation | A future allowlist regression remains possible and needs negative tests |
| Permission is silently broadened | Dedicated optional permission row, user-click request, live permission checks, revoke UI | Chrome's permission warning is category-level, not a full explanation of data loss |
| Timeout causes an unsafe retry | Indeterminate timeout result, no automatic retry, serialized cleanup | The underlying browser operation may finish after transport cancellation |
| Audit or telemetry leaks visited sites | Log digests and counts, never origins or values; bounded labels | Operators can infer that some cleanup occurred at a given time |
| Proxy or site-policy authority is smuggled into a generic command | Permissions absent, no commands/capabilities, fail-closed allowlists, release manifest tests | A separately installed malicious extension is outside this product boundary |

## Browser Matrix

| Browser | Proxy | Content settings | Browsing-data design status |
| --- | --- | --- | --- |
| Chrome 116+ desktop | Product-prohibited | Product-prohibited | Eligible after implementation, owner approval, and real-browser destructive tests |
| Edge 116+ desktop | Product-prohibited despite API support | Product-prohibited | Eligible after implementation, owner approval, and Edge release smoke tests |
| Chromium 116+ | Product-prohibited | Product-prohibited | Disabled until the exact distribution passes the Chrome test suite |
| Brave, Opera, Vivaldi, other Chromium | Product-prohibited | Product-prohibited | Unsupported until separately validated; do not infer support from API presence |
| Firefox and mobile browsers | Unsupported | Unsupported | Unsupported |

Capability negotiation is authoritative. Until implementation and approval,
all browsers must advertise these operation capabilities as unavailable.

## Verification Required Before Enablement

The future implementation cannot be released until all of these pass:

1. manifest and command-allowlist tests prove `proxy` and `contentSettings`
   are absent and generic dispatch cannot reach any reviewed API;
2. schema tests reject empty origins, paths, credentials, wildcards, duplicate
   entries, non-HTTP(S) schemes, unknown data types, invalid/future times,
   `excludeOrigins`, and protected/extension scopes;
3. policy tests cover mixed allowed/denied origin lists and fail the whole plan
   without partial deletion;
4. permission/profile tests prove no request occurs without a visible user
   click and revocation immediately removes the capability;
5. plan tests cover tampering, wrong browser/session, expiry, replay,
   disconnect, concurrent cleanup, and batch/raw rejection;
6. real Chrome and Edge tests use disposable profiles to verify each allowed
   type, subdomain cookie widening, time scopes, promise completion, and that
   unrelated origins remain intact;
7. timeout and forced-disconnect tests report indeterminate completion and
   never auto-retry;
8. logs, audit records, metrics, errors, and tracing are checked for origin,
   cookie, value, and plan-token leakage; and
9. the extension package and MCP tool reference remain unchanged when the
   feature build gate is off.

Destructive tests must never run against a developer's normal browser profile.
They require a freshly created disposable profile and synthetic origin data.

## Rollout and Recovery

The first approved rollout is opt-in development builds only. Promotion to a
release build requires a recorded product-owner decision, completed checklist
above, updated privacy disclosure, and Chrome/Edge smoke evidence. The feature
flag and permission are independently revocable. Disabling the flag removes
the MCP tools from discovery; revoking the permission removes the extension
capability immediately.

Browsing-data deletion has no rollback. Product documentation and preview
output must state that site sign-in, offline content, local application state,
and service-worker behavior may be lost. Proxy and content-settings support
cannot be enabled by configuration; they require a new code and security
review.
