# Reproducible Releases

The release pipeline creates the same byte-for-byte artifacts whenever the
source commit, version, target list, Go/Node toolchains, and locked dependencies
are unchanged.

## Supported Server Targets

Release bundles contain statically linked server binaries for:

| Operating system | Architectures |
| --- | --- |
| Linux | `amd64`, `arm64` |
| macOS (`darwin`) | `amd64`, `arm64` |
| Windows | `amd64`, `arm64` |

The extension ZIP is platform-independent and supports the Chromium versions
documented in the installation guide.

## Build a Release

Install the locked extension dependencies and build from a clean commit:

```bash
npm ci --prefix chrome-extension
make release-check
```

`make release-check` builds the bundle twice from the same inputs and compares
the complete `SHA256SUMS` files. The second build uses an isolated temporary
directory that is removed after the comparison. The final bundle remains in
`release/`.

The build derives these inputs from the repository unless explicitly
overridden:

- `VERSION` from `chrome-extension/manifest.json`;
- `COMMIT` from `git rev-parse HEAD`;
- `SOURCE_DATE_EPOCH` from the commit timestamp;
- `TARGETS` from the supported target table above;
- `RELEASE_DIR` as `release`.

The release script rejects a version that differs from the extension manifest.
Before changing the product version, update the version in `manifest.json`,
`package.json`, and both root entries in `package-lock.json`. Chrome manifest
versions contain one to four numeric components.

Linux release builders need Go, Node.js, npm, Info-ZIP `zip`, GNU `date`, GNU
`find`, and `sha256sum`. All Go and npm dependencies remain locked by `go.sum`
and `package-lock.json`.

## Release Contents

The directory contains:

- six `mcp-browser-control_<version>_<os>_<arch>` binaries;
- `mcp-browser-control_<version>_extension.zip` with normalized timestamps and
  sorted file order;
- `mcp-browser-control.cdx.json`, a deterministic CycloneDX 1.6 SBOM covering
  Go modules and locked extension build dependencies;
- `release-manifest.json` with version, source commit/date, targets, and
  artifact names;
- `RELEASE_NOTES.md`, generated from the previous reachable tag or complete
  available history;
- `SHA256SUMS` for every distributable file above.

Go binaries embed the same version, full source commit, and UTC build date. To
inspect one:

```bash
./release/mcp-browser-control_0.3.0_linux_amd64 --version
```

To verify a downloaded bundle:

```bash
cd release
sha256sum -c SHA256SUMS
```

GitHub Actions checks reproducibility and uploads the release directory for
every successful CI run. A tag matching the extension version triggers
`.github/workflows/release.yml`, which independently tests the candidate,
rebuilds the deterministic bundle, and publishes every file in `release/` to a
GitHub Release. Release artifacts are not signed until a separate signing
policy is approved.

## Publish a GitHub Release

Update the version in `chrome-extension/manifest.json`, `package.json`, and
both root entries in `package-lock.json`, commit the changes, and ensure the
`main` working tree is clean. Then run:

```bash
make publish-release VERSION=0.3.1
```

`publish-release` runs the static readiness and reproducibility checks before
creating the annotated `v0.3.1` tag. It atomically pushes `main` and the tag to
`origin`; the tag starts the release workflow. Override `RELEASE_REMOTE` or
`RELEASE_BRANCH` only when publishing from a deliberately different Git
layout:

```bash
make publish-release VERSION=0.3.1 RELEASE_REMOTE=upstream RELEASE_BRANCH=stable
```

The command rejects a dirty tree, detached HEAD, mismatched version/tag,
missing remote, wrong branch, or an existing local/remote tag. If the atomic
push fails, it removes only the local tag it just created. Re-running the
GitHub workflow updates the release notes and replaces assets with the
deterministic rebuild.

## Release Qualification

The reproducible bundle is only one release gate. Follow and sign off the
complete [`release-checklist.md`](release-checklist.md), including current
Chrome and Edge Stable tests, fresh install and upgrade flows, permission
warnings, pairing/revocation, documentation, security scans, and the published
[`known-limitations.md`](known-limitations.md).

After installing the CI-pinned security and browser tools, the complete
automated gate is:

```bash
make release-readiness
```

For a fast non-mutating check of version synchronization, the clean tree,
language policy, required documents, and generated tool reference, use:

```bash
make release-readiness-check
```

Automated Chromium E2E does not replace the checklist's manual Chrome and Edge
Stable matrix. Store submission, approvals, staged rollout, and rollback remain
recorded owner actions.
