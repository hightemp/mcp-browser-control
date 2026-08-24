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
every successful CI run. Publishing or signing a GitHub release remains a
manual owner action until a separate signing policy is approved.
