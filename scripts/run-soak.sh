#!/usr/bin/env bash
set -uo pipefail

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repository_root"

duration=${MCP_BROWSER_SOAK_DURATION:-8h}
timeout=${MCP_BROWSER_SOAK_TIMEOUT:-9h}
go_command=${GO:-go}
node_command=${NODE:-node}

echo "Starting reconnect and event soak for ${duration}"

MCP_BROWSER_SOAK_DURATION="$duration" \
  "$go_command" test -tags=soak -count=1 -v -timeout="$timeout" ./internal/soak &
go_pid=$!

MCP_BROWSER_SOAK_DURATION="$duration" \
  "$node_command" --expose-gc --test chrome-extension/tests/cdp-event-soak.js &
node_pid=$!

pids=("$go_pid" "$node_pid")
cleanup() {
  kill "${pids[@]}" 2>/dev/null || true
}
trap cleanup HUP INT TERM EXIT

status=0
while ((${#pids[@]} > 0)); do
  completed_pid=""
  if wait -n -p completed_pid "${pids[@]}"; then
    :
  else
    child_status=$?
    if ((status == 0)); then
      status=$child_status
    fi
  fi
  remaining_pids=()
  for pid in "${pids[@]}"; do
    if [[ "$pid" != "$completed_pid" ]]; then
      remaining_pids+=("$pid")
    fi
  done
  pids=("${remaining_pids[@]}")
  if ((status != 0)); then
    cleanup
  fi
done

trap - HUP INT TERM EXIT
if ((status != 0)); then
  echo "Soak failed" >&2
  exit "$status"
fi

echo "Soak completed successfully"
