# Web Storage and Origin Metadata

The storage tools operate on exactly one selected root-document HTTP(S)
origin. They use a serialized function in Chrome's isolated extension world,
following the
[Chrome scripting API](https://developer.chrome.com/docs/extensions/reference/api/scripting),
and expose only the bounded surface described here.

| MCP tool | Extension capability | Operation |
| --- | --- | --- |
| `browser_list_storage_items` | `storage.list` or `storage.listSensitive` | Page through `localStorage` or `sessionStorage` keys |
| `browser_get_storage_item` | `storage.get` or `storage.getSensitive` | Get one exact Web Storage key |
| `browser_set_storage_item` | `storage.set` | Set one exact Web Storage key without echoing its value |
| `browser_remove_storage_item` | `storage.remove` | Remove one exact Web Storage key |
| `browser_get_cache_metadata` | `storage.cacheMetadata` | Page through Cache Storage names only |
| `browser_get_indexeddb_metadata` | `storage.indexedDBMetadata` | Page through IndexedDB database names and versions only |
| `browser_clear_origin_storage` | `storage.clear` | Clear explicitly selected origin storage after confirmation |

All tools require the MCP `full` profile, Observe access to the target site,
and the Personal data profile. The implementation uses the Personal profile's
`browsingData` grant as an explicit user-consent marker; it does not invoke
`chrome.browsingData`. Actual access is through the selected page's standard
Web Storage, [CacheStorage](https://developer.mozilla.org/en-US/docs/Web/API/CacheStorage),
and [IndexedDB](https://developer.mozilla.org/en-US/docs/Web/API/IndexedDB_API)
interfaces.

## Target Scope

The caller supplies an exact origin such as `https://example.com`, never a URL
path or wildcard. Before injection, the extension verifies the selected tab,
root `documentId`, current origin, Observe grant, and Personal data grant. The
script is addressed to that one root document by `documentId`, verifies
`location.origin` inside the isolated world, and the extension repeats the
origin, permission, and document checks before returning.

`localStorage` and `sessionStorage` refer to the selected document's storage
areas. Cache and IndexedDB metadata come from that same origin. Child frames,
other tabs, other origins, extension pages, and restricted browser schemes are
not searched.

## Values and Metadata

Normal list/get calls return `[MASKED]`, `valueIncluded: false`, and the UTF-8
byte length. To read a value, enable **Sensitive data** in extension settings
and explicitly pass `includeValues: true` or `includeValue: true`. This selects
a separate sensitive extension capability. Values above 64 KiB are represented
as `[OMITTED]` even in sensitive mode.

Set accepts at most 64 KiB and never returns the supplied or previous value.
Remove reports only whether an item changed. Cache metadata contains names but
never requests, responses, headers, or bodies. IndexedDB metadata contains
database names and versions but never object-store schemas, records, keys,
indexes, cursors, handles, or blobs.

## Bounds and Pagination

- A list or metadata page defaults to 50 entries and is capped at 200.
- A storage area or metadata inventory is rejected above 10,000 entries.
- Keys and cache/database names are capped at 1 KiB of UTF-8.
- A stored Web Storage value above 1 MB makes the item or area unreadable
  through these tools; returned values and set inputs are capped at 64 KiB.
- The extension and Go server independently validate result shape and size;
  the serialized server result is capped at 1 MB.
- Pagination cursors are numeric offsets. Concurrent page changes can reorder
  results, so callers needing a stable view should avoid modifying the origin
  between pages.

These tools intentionally do not provide an unbounded export or content
stream.

## Confirmed Clear

`browser_clear_origin_storage` accepts a unique subset of `localStorage`,
`sessionStorage`, `cacheStorage`, and `indexedDB`, and requires `confirm: true`.
The isolated operation completes availability, inventory-size, and name-bound
checks for every requested type before its first mutation. It then returns only
requested/completed types, bounded counts, and deletion warnings.

The browser APIs are not transactional. A cache deletion or IndexedDB deletion
can fail or be blocked after another deletion has completed, and completed
changes cannot be rolled back. Such cases return explicit warnings and per-type
counts. This command is excluded from batch, and all storage commands are
excluded from the generic command entry point.

Audit records contain the tool name, browser/tab identity, value-mode flag,
outcome, count, and duration. They do not contain origins, keys, stored values,
cache/database names, or result content.
