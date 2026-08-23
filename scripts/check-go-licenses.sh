#!/bin/sh
set -eu

module_list=$(mktemp)
trap 'rm -f "$module_list"' EXIT HUP INT TERM

go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}}{{"\t"}}{{.Dir}}{{end}}{{end}}' \
  ./cmd/server | sort -u >"$module_list"

tab=$(printf '\t')
failed=0
while IFS="$tab" read -r module directory; do
  test -n "$module" || continue
  license_file=$(
    find "$directory" -maxdepth 1 -type f | awk -F/ '
      tolower($NF) ~ /^(un)?licen[sc]e([.].*)?$|^copying([.].*)?$|^notice([.].*)?$/ {
        print
        exit
      }
    '
  )
  if test -z "$license_file"; then
    printf 'Missing license file: %s\n' "$module" >&2
    failed=1
    continue
  fi
  if grep -Eiq \
    'GNU (AFFERO|GENERAL|LESSER) PUBLIC LICENSE|Server Side Public License|Business Source License|Commons Clause' \
    "$license_file"; then
    printf 'Forbidden dependency license: %s (%s)\n' "$module" "$license_file" >&2
    failed=1
    continue
  fi
  printf '%s\t%s\n' "$module" "${license_file##*/}"
done <"$module_list"

exit "$failed"
