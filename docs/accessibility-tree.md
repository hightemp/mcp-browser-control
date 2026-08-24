# Accessibility Tree

Status: implemented as a typed, bounded CDP-backed tool

Last reviewed: 2026-08-24

`browser_get_accessibility_tree` returns a normalized accessibility tree for
one selected tab. It is available only in the MCP `full` profile and requires
both Observe site access and the explicitly granted Debug permission.

The implementation follows the official Chrome DevTools Protocol
Accessibility domain:

- `Accessibility.getFullAXTree` retrieves the root document tree with a bounded
  `depth`;
- `Accessibility.getPartialAXTree` retrieves a previously reported positive
  `backendNodeId` and can include its relatives;
- `Page.getFrameTree` supplies bounded frame metadata and the root CDP frame
  identity.

Only those exact methods are leased from the shared CDP Session Manager. The
tool does not enable accessibility event streaming, evaluation, DOM mutation,
or a raw CDP path. The protocol methods remain marked experimental upstream,
so capability advertisement and release smoke tests remain mandatory.

## Input and selection

`mode` defaults to `full`. Full mode accepts `maxDepth`; partial mode instead
requires `backendNodeId` and accepts `fetchRelatives`. A caller can bind the
request to the current root `documentId`, which makes navigation fail with
`STALE_TARGET` rather than returning a tree from a replacement document.

Post-retrieval filters include:

- case-insensitive exact roles;
- a case-insensitive accessible-name substring;
- inclusion or exclusion of ignored nodes.

The extension repeats tab existence, HTTP/HTTPS origin access, Debug
permission, and root document checks immediately around the managed CDP calls.

## Normalized output

The response contains:

- the tab and root document identity;
- a bounded flat frame list with parent linkage and query/fragment-free URLs;
- a bounded flat AX node list with `nodeId`, `parentId`, depth, ignored state,
  role, accessible name, description, value, normalized properties,
  `backendNodeId`, and associated CDP `frameId`;
- total, matching, and returned node counts plus explicit truncation warnings.

Role/name locator hints are included by default. When the role/name pair is
unique in the returned source tree, the extension may resolve it through the
existing page bridge and attach a temporary root-document `reference`. That
reference can be passed back through the normal locator shape as
`{"element": reference}`. Duplicate pairs retain a role/name locator with a
bounded `nth` hint but do not receive an element reference. Child-frame nodes
keep their CDP frame association but do not receive a potentially ambiguous
root-frame reference.

## Limits and redaction

Defaults and hard limits are:

| Limit | Default | Hard maximum |
| --- | ---: | ---: |
| Full-tree depth | 20 | 50 |
| Returned nodes | 1,000 | 5,000 |
| Properties per node | 20 | 50 |
| Characters per normalized value | 500 | 2,000 |
| Element reference lookups | 50 | 100 |
| Normalized result bytes | 1,500,000 | 1,500,000 |
| Scanned CDP nodes | 20,000 | 20,000 |
| Returned frames | 100 | 100 |

The session manager independently rejects a raw CDP command result above its
4 MiB bound. The extension then normalizes scalar values, removes raw AX value
sources and related-node payloads, redacts protected or secret-identified node
values, and trims output to the requested byte budget. The Go server validates
the returned shape, counts, references, locators, string/property bounds, and
requested byte limit before applying its independent result redaction and MCP
output bound.

## Known limitations

- The full tree is the root document tree returned by the browser. Nodes carry
  frame association, but the tool does not recursively attach to child targets
  to merge separate out-of-process iframe trees.
- Element references are best-effort and limited to unambiguous root-frame
  role/name matches. `backendNodeId` remains available for a later partial-tree
  request within the same live document.
- Real Chrome and Edge smoke tests remain part of release qualification because
  the upstream Accessibility domain is experimental.

Source: [Chrome DevTools Protocol Accessibility domain](https://chromedevtools.github.io/devtools-protocol/tot/Accessibility/).
