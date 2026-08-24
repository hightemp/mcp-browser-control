#!/bin/sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repository_root"

version=${VERSION:?VERSION is required}
commit=${COMMIT:?COMMIT is required}
source_date_epoch=${SOURCE_DATE_EPOCH:?SOURCE_DATE_EPOCH is required}
targets=${TARGETS:?TARGETS is required}
release_directory=${RELEASE_DIR:-release}

case "$version" in
  *[!0-9.]* | .* | *. | *..*)
    echo "VERSION must be a dotted numeric Chrome manifest version" >&2
    exit 1
    ;;
esac
case "$commit" in
  *[!0-9a-f]*)
    echo "COMMIT must be a full 40-character Git object ID" >&2
    exit 1
    ;;
esac
if [ "${#commit}" -ne 40 ]; then
  echo "COMMIT must be a full 40-character Git object ID" >&2
  exit 1
fi
case "$source_date_epoch" in
  *[!0-9]* | "")
    echo "SOURCE_DATE_EPOCH must be a Unix timestamp" >&2
    exit 1
    ;;
esac

release_directory=$(realpath -m -- "$release_directory")
case "$release_directory" in
  "$repository_root"/release | "$repository_root"/release-* | "$repository_root"/.release-*) ;;
  *)
    echo "RELEASE_DIR must be a release, release-*, or .release-* directory in the repository root" >&2
    exit 1
    ;;
esac

build_date=$(date -u --date="@$source_date_epoch" +%Y-%m-%dT%H:%M:%S.000Z)
manifest_version=$(node -p "require('./chrome-extension/manifest.json').version")
if [ "$manifest_version" != "$version" ]; then
  echo "VERSION $version does not match chrome-extension/manifest.json $manifest_version" >&2
  exit 1
fi

rm -rf -- "$release_directory"
mkdir -p -- "$release_directory"

linker_flags="-s -w -buildid= -X github.com/hightemp/go_mcp_browser_ext_tool/internal/app.Version=$version -X github.com/hightemp/go_mcp_browser_ext_tool/internal/app.Commit=$commit -X github.com/hightemp/go_mcp_browser_ext_tool/internal/app.BuildDate=$build_date"
for target in $targets; do
  goos=${target%/*}
  goarch=${target#*/}
  if [ "$goos" = "$target" ] || [ -z "$goos" ] || [ -z "$goarch" ]; then
    echo "invalid release target: $target" >&2
    exit 1
  fi
  suffix=
  if [ "$goos" = windows ]; then
    suffix=.exe
  fi
  output="$release_directory/mcp-browser-control_${version}_${goos}_${goarch}${suffix}"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -mod=readonly -trimpath -buildvcs=false -ldflags="$linker_flags" \
    -o "$output" ./cmd/server
done

MCP_BROWSER_VERSION="$version" npm run build --prefix chrome-extension
extension_directory="$repository_root/chrome-extension/dist/extension"
find "$extension_directory" -type f -exec touch -d "@$source_date_epoch" {} +
extension_zip="$release_directory/mcp-browser-control_${version}_extension.zip"
(
  cd "$extension_directory"
  find . -type f -print | LC_ALL=C sort | TZ=UTC zip -X -q "$extension_zip" -@
)

VERSION="$version" \
COMMIT="$commit" \
SOURCE_DATE_EPOCH="$source_date_epoch" \
TARGETS="$targets" \
RELEASE_DIR="$release_directory" \
  node scripts/generate-release-metadata.mjs

(
  cd "$release_directory"
  find . -maxdepth 1 -type f ! -name SHA256SUMS -printf '%P\n' |
    LC_ALL=C sort |
    while IFS= read -r artifact; do
      sha256sum "$artifact"
    done >SHA256SUMS
)

node -e 'JSON.parse(require("node:fs").readFileSync(process.argv[1], "utf8"))' \
  "$release_directory/mcp-browser-control.cdx.json"
echo "Built reproducible release $version in $release_directory"
