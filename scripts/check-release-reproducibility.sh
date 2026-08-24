#!/bin/sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repository_root"

release_directory=$(realpath -m -- "${RELEASE_DIR:-release}")
if [ ! -f "$release_directory/SHA256SUMS" ]; then
  echo "build the primary release before checking reproducibility" >&2
  exit 1
fi

comparison_directory="$repository_root/.release-reproducibility-$$"
case "$comparison_directory" in
  "$repository_root"/.release-reproducibility-*) ;;
  *) exit 1 ;;
esac
cleanup() {
  rm -rf -- "$comparison_directory"
}
trap cleanup EXIT HUP INT TERM

VERSION=${VERSION:?VERSION is required} \
COMMIT=${COMMIT:?COMMIT is required} \
SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH:?SOURCE_DATE_EPOCH is required} \
TARGETS=${TARGETS:?TARGETS is required} \
RELEASE_DIR="$comparison_directory" \
  sh scripts/build-release.sh

diff -u "$release_directory/SHA256SUMS" "$comparison_directory/SHA256SUMS"
echo "Reproducibility check passed for $VERSION"
