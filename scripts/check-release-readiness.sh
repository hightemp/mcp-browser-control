#!/bin/sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repository_root"

fail() {
  echo "release readiness: $*" >&2
  exit 1
}

for required_file in \
  README.md \
  chrome-extension/INSTALL.md \
  docs/browser-support.md \
  docs/known-limitations.md \
  docs/performance-soak.md \
  docs/release-checklist.md \
  docs/releasing.md \
  docs/security-review.md \
  docs/tool-reference.md; do
  test -s "$required_file" || fail "missing required file: $required_file"
done

if [ "${RELEASE_ALLOW_DIRTY:-0}" != "1" ]; then
  changes=$(git status --porcelain --untracked-files=all)
  test -z "$changes" || fail "the candidate working tree is not clean"
fi

node <<'NODE'
const fs = require("node:fs");

const manifest = JSON.parse(fs.readFileSync("chrome-extension/manifest.json", "utf8"));
const packageJSON = JSON.parse(fs.readFileSync("chrome-extension/package.json", "utf8"));
const packageLock = JSON.parse(fs.readFileSync("chrome-extension/package-lock.json", "utf8"));
const version = manifest.version;

if (!/^\d+(?:\.\d+){0,3}$/.test(version)) {
  throw new Error("manifest version is not Chrome-compatible");
}
if (
  packageJSON.version !== version ||
  packageLock.version !== version ||
  packageLock.packages?.[""]?.version !== version
) {
  throw new Error("manifest/package/lock versions do not match");
}
if (manifest.manifest_version !== 3 || manifest.minimum_chrome_version !== "116") {
  throw new Error("the release must remain MV3 with minimum Chrome 116");
}
NODE

set +e
cyrillic_matches=$(git grep -I -n -P '[\x{0400}-\x{052F}]' -- . \
  ':(exclude)AGENTS.md' ':(exclude)TASKS.md' ':(exclude)PRD.md' 2>&1)
cyrillic_status=$?
set -e
case "$cyrillic_status" in
  0)
    echo "$cyrillic_matches" >&2
    fail "Cyrillic text exists outside the three approved planning files"
    ;;
  1) ;;
  *) fail "the Cyrillic language gate could not inspect tracked files" ;;
esac

grep -Fq 'docs/known-limitations.md' README.md ||
  fail "README does not link the published limitations"
grep -Fq 'release-checklist.md' docs/releasing.md ||
  fail "the release guide does not link the release checklist"
grep -Fq 'known-limitations.md' docs/release-checklist.md ||
  fail "the checklist does not require a limitations review"

go run ./cmd/tool-reference -check -output docs/tool-reference.md

if [ "${RELEASE_REQUIRE_ARTIFACTS:-0}" = "1" ]; then
  test -s release/SHA256SUMS || fail "release/SHA256SUMS is missing"
  test -s release/release-manifest.json || fail "release manifest is missing"
  test -s release/mcp-browser-control.cdx.json || fail "release SBOM is missing"
  test -s release/RELEASE_NOTES.md || fail "release notes are missing"
  (
    cd release
    sha256sum -c SHA256SUMS
  )
fi

echo "Static release readiness checks passed"
