#!/usr/bin/env bash
set -euo pipefail

if [[ "${MCP_BROWSER_SOAK_FAKE_CHILD:-0}" == "1" ]]; then
  marker_directory=${MCP_BROWSER_SOAK_MARKER_DIRECTORY:?}
  printf '%s\n' "$$" >>"${marker_directory}/child-pids"
  exec sleep 3600
fi

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
temporary_directory=$(mktemp -d)
output_file="${temporary_directory}/output"
child_pid_file="${temporary_directory}/child-pids"

cleanup() {
  if [[ -f "$child_pid_file" ]]; then
    while IFS= read -r child_pid; do
      if [[ "$child_pid" =~ ^[0-9]+$ ]]; then
        kill "$child_pid" 2>/dev/null || true
      fi
    done <"$child_pid_file"
  fi
  rm -rf -- "$temporary_directory"
}
trap cleanup EXIT

set +e
timeout --preserve-status --signal=INT --kill-after=2s 0.5s \
  env \
  MCP_BROWSER_SOAK_DURATION=1h \
  MCP_BROWSER_SOAK_TIMEOUT=2h \
  MCP_BROWSER_SOAK_FAKE_CHILD=1 \
  MCP_BROWSER_SOAK_MARKER_DIRECTORY="$temporary_directory" \
  GO="$repository_root/scripts/test-run-soak-interrupt.sh" \
  NODE="$repository_root/scripts/test-run-soak-interrupt.sh" \
  bash "$repository_root/scripts/run-soak.sh" >"$output_file" 2>&1
harness_status=$?
set -e

if ((harness_status != 130)); then
  cat "$output_file" >&2
  echo "soak harness test: expected SIGINT exit status 130, got ${harness_status}" >&2
  exit 1
fi

if grep -Fq "unbound variable" "$output_file"; then
  cat "$output_file" >&2
  echo "soak harness test: interrupted wait accessed an unset variable" >&2
  exit 1
fi

if ! grep -Fq "Soak interrupted" "$output_file"; then
  cat "$output_file" >&2
  echo "soak harness test: missing interruption diagnostic" >&2
  exit 1
fi

if [[ ! -f "$child_pid_file" ]] || [[ $(wc -l <"$child_pid_file") -ne 2 ]]; then
  cat "$output_file" >&2
  echo "soak harness test: both child processes did not start" >&2
  exit 1
fi

while IFS= read -r child_pid; do
  if kill -0 "$child_pid" 2>/dev/null; then
    cat "$output_file" >&2
    echo "soak harness test: child ${child_pid} survived harness interruption" >&2
    exit 1
  fi
done <"$child_pid_file"

echo "Soak harness interruption test passed"
