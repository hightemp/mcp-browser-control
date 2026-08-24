# Cookie Tools

Cookie management is exposed through four typed MCP tools. It uses the
[Chrome cookies API](https://developer.chrome.com/docs/extensions/reference/api/cookies)
and does not use CDP.

| MCP tool | Extension capability | Behavior |
| --- | --- | --- |
| `browser_list_cookies` | `cookies.list` or `cookies.listSensitive` | Filtered, paginated metadata for cookies visible to one URL |
| `browser_get_cookie` | `cookies.get` or `cookies.getSensitive` | Zero or one named cookie visible to one URL |
| `browser_set_cookie` | `cookies.set` | Set one cookie without echoing the supplied value |
| `browser_remove_cookie` | `cookies.remove` | Remove one named cookie and report whether it existed |

All four tools require the MCP `full` profile, the optional Personal data
`cookies` permission, Observe access to the target HTTP(S) origin, and the Core
`tabs` and `webNavigation` APIs. Permission and feature-flag changes update the
advertised capabilities without an extension reload or browser reconnect.

## Scope and Target Identity

Every call resolves one browser, one tab, its root document, and the cookie
store containing that tab. The supplied `url` must have exactly the same origin
as the current root tab. A supplied `documentId` rejects a call after
navigation. The extension verifies the origin, document, permissions, and
store before the cookie API call and verifies them again before returning.

An explicit `storeId` is accepted only when that store contains the selected
tab. This keeps normal and incognito stores isolated. The server action policy
also checks the requested URL and any partition top-level site before browser
dispatch. Browser-internal schemes, extension stores, credentials in URLs,
cross-origin requests, and child-frame targets are rejected.

## Values and Sensitive Data Mode

Cookie values are masked as `[MASKED]` by default. Results still report the
UTF-8 byte length and `valueIncluded: false`. Setting a cookie never returns the
caller-supplied value.

Unmasked reads require both of these explicit choices:

1. Enable **Allow cookie values in explicitly requested sensitive-data
   results** in extension settings.
2. Set `includeValues: true` on `browser_list_cookies`, or `includeValue: true`
   on `browser_get_cookie`.

The extension advertises separate `cookies.listSensitive` and
`cookies.getSensitive` capabilities only while the setting is enabled. The Go
server selects those commands only for an explicit unmasked request and
validates their wire shape independently instead of sending them through the
normal cookie-array redaction rule. A sensitive command cannot be invoked
through `browser_send_command` or `browser_batch`.

Inline values are limited to 4,096 UTF-8 bytes. An unexpectedly larger browser
value is represented as `[OMITTED]`, never returned inline, retains only its
bounded byte length, and produces a warning.
Control characters and unsafe cookie syntax are rejected when setting values.

## Filters, Pagination, and Bounds

List supports URL, domain, name, path, Secure, session, store, and partition-key
filters. A domain filter must be the URL host or one of its parent domains.
The default page size is 50 and the maximum is 200. `nextCursor` is a positive
offset for the next result page. The extension rejects a browser response above
10,000 scanned cookies, returns at most 200 cookies, and the server rejects a
serialized cookie result above 1,000,000 bytes.

Cookie names, domains, paths, store IDs, warnings, document IDs, numeric values,
and every result field have independent bounds and shapes in the command
router, extension handler, and Go server. Extension results with unknown fields,
an incorrect target, an unmasked value on a normal command, or inconsistent
session/expiration metadata fail closed.

## Set and Remove

Set accepts a bounded name and value plus optional domain, path, Secure,
HttpOnly, SameSite, expiration, store, and partition metadata. A domain must
contain the request host. `SameSite=None` requires `secure: true`. Omitting
`expirationDate` creates a session cookie. The normalized result is always
masked.

Remove accepts URL, name, store, and optional partition metadata. It removes at
most one matching cookie and returns only `removed: true` or `false` plus target
metadata.

## Partitioned Cookies

`partitionKey.topLevelSite` must equal the selected root-document origin.
`hasCrossSiteAncestor` is optional and is preserved when the browser exposes
it. Chrome added partition-key filtering in version 119 and the
`hasCrossSiteAncestor` field in version 130; unpartitioned cookie operations
remain available on the project's Chrome/Edge 116 compatibility floor.
Consumers should omit unsupported partition fields and handle a safe browser
API error on older compatible releases.

## Audit and Residual Risk

Audit records contain only the tool name, bounded browser ID, tab ID, whether
values were requested, result count, outcome, and duration. They never contain
URLs, names, domains, values, filters, request arguments, or result bodies.

An authenticated `full` client with Personal data and site access can read or
change session state for allowed origins. Sensitive data mode intentionally
permits secret-bearing values to cross the MCP boundary. Keep it disabled when
not actively needed, grant Observe only to necessary sites, remove the Personal
data profile after use, and treat all unmasked MCP results as secrets.
