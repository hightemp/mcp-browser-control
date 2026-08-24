# Known Limitations

Status: published for release qualification

Last reviewed: 2026-08-24

This page lists product boundaries that must be visible in release notes and
reviewed before every release. A limitation may be removed only after its task,
security review, browser matrix, tests, and user documentation are complete.

## Connectivity and Browser Support

- The server is local-only. Remote listening and remote browser control are not
  implemented; loopback binding is a mandatory security boundary.
- Desktop Google Chrome Stable and Microsoft Edge Stable based on Chromium 116
  or newer are the supported browsers. Other desktop Chromium products are
  best effort. Firefox, Safari, and mobile browsers are not supported.
- CDP-backed features require the optional Debug permission and can conflict
  with another debugger or an open DevTools attachment. The tool returns a
  capability error instead of taking over an existing attachment.
- Child-target CDP sessions require Chromium 125 or newer. Root-document
  capabilities remain available on the documented minimum version.

## Automation Boundaries

- DOM interaction uses the content-script backend. Explicit trusted CDP input
  is not yet implemented.
- Screenshots capture the viewport only. Full-page and element screenshots are
  not implemented.
- JavaScript evaluation is isolated-world, JSON-only, bounded, and disabled by
  default. Main-world, persistent, and unrestricted evaluation are prohibited.
- Raw CDP is disabled by default and limited to the reviewed read-only method
  allowlist. It is not a general DevTools Protocol escape hatch.
- Network capture does not intercept, block, replay, or modify requests. Body
  access is active-capture-only, same-origin, textual, MIME-allowlisted, and
  bounded; HAR-like exports exclude bodies.
- Performance captures are short-lived and bounded. Heap snapshots,
  caller-controlled trace categories, stream handles, and continuous profiling
  are not implemented.

## Personal and Browser-Wide Data

- Download, history, bookmark, and reading-list MCP tools are not implemented
  yet, even though their permissions are represented by the optional Personal
  data profile. Cookie and Web Storage tools are implemented for exact HTTP(S)
  origins; partition-key fields depend on the browser version. Storage access
  is bounded to Web Storage items, Cache Storage names, IndexedDB names and
  versions, and explicitly confirmed origin clearing—records and blobs are not
  exposed.
- Clipboard and file-input automation are not implemented. MCP calls never
  bypass browser user-activation or native file-picker requirements.
- Proxy and content-settings controls are prohibited. Browsing-data deletion
  is not enabled and requires the separately documented product and security
  approval before implementation.
- The server never reads downloaded file contents or accepts arbitrary local
  paths from MCP clients.

## Release and Operations

- Store submission, staged rollout, signing, and GitHub Release publication are
  manual owner actions. The repository builds deterministic unsigned artifacts,
  checksums, release notes, a release manifest, and a CycloneDX SBOM.
- Chrome and Edge store review timelines and enterprise update policies are
  outside the project's control. Validate both a clean install and an update in
  real stable browsers for every release candidate.
- Temporary artifacts are local, owner-scoped, quota-limited, and expire. They
  are not a durable archive and may still contain sensitive page data after
  redaction.

The detailed capability and browser matrix is maintained in
[`browser-support.md`](browser-support.md). Security prohibitions and conditional
future work are maintained in [`security-review.md`](security-review.md).
