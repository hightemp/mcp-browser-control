# History, Bookmarks, and Reading List

Status: implemented as optional Personal data capabilities

Last reviewed: 2026-08-24

These tools are browser-scoped. They require the MCP `full` tool profile and
the matching optional extension permission. They do not require a selected tab
or website access. The extension advertises each domain only while both its API
surface and permission are available.

## Tools

| Domain | Read tools | Mutation tools |
| --- | --- | --- |
| History | `browser_search_history`, `browser_get_history_visits` | `browser_delete_history_url`, `browser_delete_history_range`, `browser_clear_history` |
| Bookmarks | `browser_list_bookmarks` | `browser_create_bookmark`, `browser_update_bookmark`, `browser_move_bookmark`, `browser_remove_bookmark` |
| Reading List | `browser_list_reading_list` | `browser_add_reading_list_entry`, `browser_update_reading_list_entry`, `browser_remove_reading_list_entry` |

History mutations always require `confirm: true`. Removing a single bookmark
or empty folder does not; recursive folder-tree removal requires both
`recursive: true` and `confirm: true`. Reading-list removal affects one exact
URL and is not treated as bulk deletion.

Bookmark creation without `url` creates a folder. Bookmark create/update URLs,
history URL operations, and reading-list mutations accept only bounded
HTTP(S) URLs without credentials. Bookmark root and managed permanent folders
remain subject to the browser API's own restrictions.

## Pagination and Bounds

Read tools use opaque-in-contract decimal offset cursors. A response includes
`nextCursor` only when another page is available. The default page size is 50,
the maximum page size is 200, and one request scans at most 10,000 browser
records. If an API returns more than that bound, the command fails with
`PAYLOAD_TOO_LARGE` instead of returning a partial, ambiguous inventory.

History search is performed by Chrome with a maximum of 10,001 candidates so
the extension can detect overflow. The Bookmarks and Reading List APIs do not
provide native cursor pagination, so the extension receives the bounded result
set, validates it, applies a deterministic page, and discards it after the
request. Reading-list entries are sorted by last update time and URL because
Chrome does not guarantee API result order.

Offset cursors are not snapshots. Concurrent history, bookmark, or reading-list
changes can shift later pages. Callers that need a fresh view should restart
without a cursor.

## Data Handling

The extension normalizes every returned object and the server rejects unknown
or oversized response shapes. Returned URLs:

- remove embedded credentials;
- remove fragments; and
- replace values of password, token, secret, credential, authorization,
  cookie, and API-key-shaped query parameters with `[REDACTED]`.

The Go server independently rejects sensitive query parameters that were not
redacted. Titles and non-sensitive URL components are intentionally returned
because they are the requested Personal data; MCP clients must treat the whole
result as sensitive.

Audit records contain only the fixed domain/tool name, bounded browser ID,
item count, outcome, and duration. Search text, titles, URLs, visit IDs,
bookmark IDs, and result bodies are never logged. Dedicated commands are
blocked from `browser_send_command` and every Personal data tool is excluded
from `browser_batch` to prevent confirmation or output-bound bypasses.

Permission availability is rechecked before and after each browser API
operation. Revocation during a request fails closed. Cancellation and timeout
stop result processing, although a browser mutation that already completed
cannot be rolled back.

## Browser Support

- Chrome and Chromium-family history support uses the optional `history`
  permission and the documented `search`, `getVisits`, `deleteUrl`,
  `deleteRange`, and `deleteAll` methods.
- Bookmark support uses the optional `bookmarks` permission and the documented
  search, children, create, update, move, remove, and recursive-remove methods.
- Reading List requires the optional `readingList` permission and an API that
  exposes query/add/update/remove. Chrome documents this API as Chrome 120+ and
  notes that entries are keyed by exact URL, including query and fragment.
  The project removes fragments from returned metadata, so callers must retain
  the exact input URL for later mutation when fragment identity matters.
- Edge and other Chromium browsers remain capability-gated until release smoke
  testing confirms the corresponding API and permission behavior.

Official references:

- [Chrome History API](https://developer.chrome.com/docs/extensions/reference/api/history)
- [Chrome Bookmarks API](https://developer.chrome.com/docs/extensions/reference/api/bookmarks)
- [Chrome Reading List API](https://developer.chrome.com/docs/extensions/reference/api/readingList)
