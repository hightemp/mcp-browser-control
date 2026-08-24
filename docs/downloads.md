# Download Management

The download tools expose a deliberately narrow subset of the
[Chrome downloads API](https://developer.chrome.com/docs/extensions/reference/api/downloads).
They manage lifecycle and bounded status metadata without reading downloaded
files or returning absolute local paths.

| MCP tool | Extension capability | Operation |
| --- | --- | --- |
| `browser_list_downloads` | `downloads.list` | Page through recent download status metadata |
| `browser_get_download` | `downloads.get` | Get one persistent download ID |
| `browser_create_download` | `downloads.create` | Start one constrained HTTP(S) download |
| `browser_pause_download` | `downloads.pause` | Pause one active unpaused download |
| `browser_resume_download` | `downloads.resume` | Resume one browser-reported resumable download |
| `browser_cancel_download` | `downloads.cancel` | Cancel one active download |
| `browser_erase_download_history` | `downloads.erase` | Erase one terminal history entry after confirmation |

Every tool requires the optional Personal data `downloads` permission and the
MCP `full` profile. Capabilities disappear immediately when permission is
revoked. They are browser-scoped and may use an explicit `browserId`; they do
not accept a tab or page target.

## Safe Metadata

List/get/lifecycle results include the persistent ID, state, paused/resumable
flags, danger/error classification, byte counts, MIME type, timestamps,
existence hint, and incognito flag when server policy permits that context.
Source and final HTTP(S) URLs have credentials, query, and fragment removed.
Non-HTTP(S) historical URLs are represented as `[REDACTED]`.

Chrome supplies `DownloadItem.filename` as an absolute local path. The
extension strips every directory component before the result crosses the
extension boundary and returns only `fileName` plus `pathRedacted: true`. The
Go server independently rejects separators or an unredacted path shape. A
basename may still reveal personal information, so normal server redaction and
bounded-string rules remain active.

The list page defaults to 50 and is capped at 200. Search scans at most 10,000
records, URL metadata is capped at 8 KiB, basenames at 1 KiB, other strings at
256 bytes, and the final extension/server result at 1 MB. Numeric cursors are
offsets over newest-first results; concurrent history changes can shift later
pages.

Chrome documents `exists` as an eventually refreshed hint rather than an
instant filesystem guarantee. These tools expose that browser value but never
attempt an independent filesystem check.

## Creating a Download

Create accepts one bounded HTTP(S) URL. Server action policy rejects restricted
schemes, browser extension stores, denylisted origins, and origins outside a
configured allowlist before browser dispatch. The extension independently
rejects non-HTTP(S), credential-bearing, and browser-store URLs.

The call always uses `conflictAction: "uniquify"` and `saveAs: false`. Callers
cannot provide a filename, directory, absolute path, request method, body,
headers, or an automatic danger-acceptance option. Chrome states that an HTTP(S)
download includes cookies already stored for the destination hostname; the
result therefore includes a sensitivity warning. The normal result contains
only the new download ID, never the source URL or a local path.

## Lifecycle and History

Pause requires an active unpaused item, resume requires `canResume`, and cancel
requires an active item. The extension searches and validates the exact ID
before invoking the lifecycle method and returns bounded status afterward.
Missing IDs use `DOWNLOAD_NOT_FOUND`.

History erase is intentionally single-item, terminal-state-only, and requires
`confirm: true`. Chrome's `downloads.erase()` removes the matching
`DownloadItem` from browser history but does not delete the downloaded file;
the result states that explicitly. Active downloads must be cancelled first.

The following API surfaces are not exported:

- `removeFile`, file opening, folder reveal, file icons, and file-content reads;
- `acceptDanger` and any bypass of Chrome's dangerous-download UI;
- bulk or filter-based history erase;
- caller-chosen filenames, paths, headers, methods, or request bodies;
- generic-command or batch access.

Incognito records are filtered or rejected unless the server was explicitly
started with incognito action policy enabled. Audit records contain the tool,
browser ID, numeric download ID, count, outcome, and duration only. They never
contain URLs, filenames, paths, MIME values, or file content.
