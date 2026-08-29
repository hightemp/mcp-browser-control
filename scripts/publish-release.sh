#!/bin/sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repository_root"

fail() {
  echo "publish release: $*" >&2
  exit 1
}

version=${VERSION:?VERSION is required}
tag=${RELEASE_TAG:-v$version}
remote=${RELEASE_REMOTE:-origin}
branch=${RELEASE_BRANCH:-main}

case "$version" in
  *[!0-9.]* | .* | *. | *..*)
    fail "VERSION must be a dotted numeric Chrome manifest version"
    ;;
esac

version_components=$(printf '%s\n' "$version" | awk -F. '{ print NF }')
test "$version_components" -le 4 ||
  fail "VERSION must contain at most four numeric components"

expected_tag="v$version"
test "$tag" = "$expected_tag" ||
  fail "RELEASE_TAG must be $expected_tag for VERSION $version"

manifest_version=$(node -p "require('./chrome-extension/manifest.json').version")
test "$manifest_version" = "$version" ||
  fail "VERSION $version does not match chrome-extension/manifest.json $manifest_version"

changes=$(git status --porcelain --untracked-files=all)
test -z "$changes" || fail "the working tree must be clean"

current_branch=$(git symbolic-ref --quiet --short HEAD) ||
  fail "HEAD must be attached to $branch"
test "$current_branch" = "$branch" ||
  fail "releases must be published from $branch, not $current_branch"

git remote get-url "$remote" >/dev/null 2>&1 ||
  fail "Git remote $remote does not exist"

if git show-ref --verify --quiet "refs/tags/$tag"; then
  fail "local tag $tag already exists"
fi

set +e
git ls-remote --exit-code --tags "$remote" "refs/tags/$tag" >/dev/null 2>&1
remote_tag_status=$?
set -e
case "$remote_tag_status" in
  0) fail "remote tag $tag already exists" ;;
  2) ;;
  *) fail "could not inspect tags on remote $remote" ;;
esac

git tag -a "$tag" -m "Release $tag"
if ! git push --atomic "$remote" "$branch" "refs/tags/$tag"; then
  git tag -d "$tag" >/dev/null
  fail "atomic push failed; removed the local $tag tag"
fi

echo "Published $tag from $branch to $remote"
echo "GitHub Actions will build and publish the release artifacts."
