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
status=0
interrupted_status=0

cleanup() {
  if ((${#pids[@]} > 0)); then
    kill "${pids[@]}" 2>/dev/null || true
  fi
}

drain_children() {
  local pid
  for pid in "${pids[@]}"; do
    wait "$pid" 2>/dev/null || true
  done
  pids=()
}

handle_signal() {
  interrupted_status=$1
  if ((status == 0)); then
    status=$interrupted_status
  fi
  cleanup
}

trap 'handle_signal 129' HUP
trap 'handle_signal 130' INT
trap 'handle_signal 143' TERM
trap cleanup EXIT

while ((${#pids[@]} > 0)); do
  completed_pid=""
  if wait -n -p completed_pid "${pids[@]}"; then
    child_status=0
  else
    child_status=$?
  fi

  # Bash may unset the variable named by wait -p when a trapped signal
  # interrupts wait before a child is reaped. Stop once on every signal or
  # child failure, then explicitly wait for all known children. This also
  # handles jobs Bash has already removed from its wait table.
  if ((interrupted_status != 0 || child_status != 0)); then
    if ((status == 0)); then
      status=$child_status
    fi
    cleanup
    drain_children
    break
  fi

  if [[ -z "${completed_pid:-}" ]]; then
    status=1
    cleanup
    drain_children
    break
  fi

  remaining_pids=()
  for pid in "${pids[@]}"; do
    if [[ "$pid" != "$completed_pid" ]]; then
      remaining_pids+=("$pid")
    fi
  done
  pids=("${remaining_pids[@]}")
done

trap - HUP INT TERM EXIT
if ((status != 0)); then
  if ((interrupted_status != 0)); then
    echo "Soak interrupted" >&2
  else
    echo "Soak failed" >&2
  fi
  exit "$status"
fi

echo "Soak completed successfully"
