# Performance and Soak Testing

This project has machine-checked latency budgets and a configurable long-run
test for browser reconnects and bounded event delivery.

## Acceptance Budgets

| Signal | Budget | Enforcement |
| --- | ---: | --- |
| Router overhead, p95 | `< 50 ms` | 10,000 in-process request/response samples |
| `browser_list` with 50 registered browsers, p95 | `< 100 ms` | 1,000 complete tool-handler samples, including result serialization |
| Incorrectly routed commands | `0 / 10,000` | Concurrent four-browser router stress test |
| Reconnect success | `>= 99.5%` | Authenticated WebSocket reconnect loop |
| Transport event loss | `0` | Ordered event burst followed by an acknowledged protocol ping |
| Retained Go heap growth | `<= 32 MiB` | Forced-GC start/end samples |
| Retained Go goroutine growth | `<= 16` | Start/end runtime samples after shutdown |
| Retained extension heap growth | `<= 32 MiB` | Forced-GC start/end samples |
| CDP event queue | `<= 32 events` in the soak profile | Live queue statistics and drop accounting |

The latency budgets intentionally measure server overhead only. Browser page
work, Chrome scheduling, network navigation, and extension command execution
are outside those two NFRs.

## Commands

Run the latency gates and allocation benchmarks:

```sh
make performance
```

Run the short CI/development qualification:

```sh
make soak-smoke
```

Run the release-candidate soak. The Go WebSocket reconnect load and the
extension CDP event load run concurrently, so this takes eight hours rather
than sixteen:

```sh
mkdir -p .soak-results
make soak 2>&1 | tee ".soak-results/soak-$(date -u +%Y%m%dT%H%M%SZ).log"
```

The long-run duration can be overridden for investigation:

```sh
make soak SOAK_DURATION=30m SOAK_TIMEOUT=45m
```

The harness prints one `SOAK_REPORT` JSON object for each component. Preserve
the complete log with the commit SHA, operating system, Go version, Node.js
version, Chrome/Edge version used by the preceding E2E gate, and whether the
host was otherwise idle. A release candidate passes only when both processes
exit successfully and the JSON values meet the table above.

## What Is Exercised

The Go component repeatedly closes and authenticates the same persistent
`browserId`, verifies that every reconnect gets a new connection, sends bounded
event bursts, and uses a protocol ping as an ordering barrier. It records
reconnect and pong p95 latency, event totals, heap usage, and goroutine counts.

The extension component uses the production `CDPSessionManager` with a slow
consumer and sustained `Network.requestWillBeSent` bursts. It verifies item and
byte queue bounds, checks that every emitted event is either delivered or
reported through `droppedBefore`, and records delivery p95, intentional drops,
queue high-water mark, and heap usage. Drops in this component demonstrate
bounded backpressure and must be fully accounted for; they are not transport
loss.

`make soak-smoke` is part of CI. The eight-hour command is a release-candidate
qualification because running it on every push would serialize normal
development for most of a workday.

## Implementation Smoke Baseline

The implementation baseline below was captured on 2026-08-24 on Linux
7.0.0 x86-64 with Go 1.26.7 and Node.js 22.16.0. It proves that the harness and
budgets work on the development host; it does not replace the candidate-specific
eight-hour evidence.

| Measurement | Result |
| --- | ---: |
| Router latency p95, 10,000 samples | 3.725 us |
| `browser_list` latency p95, 50 browsers/1,000 samples | 442.579 us |
| Router benchmark | 1,986 ns/op, 1,417 B/op, 27 allocs/op |
| `browser_list` benchmark | 271,428 ns/op, 97,868 B/op, 430 allocs/op |
| Reconnects over 5 seconds | 185/185 (100%) |
| Reconnect / pong p95 | 0.593 ms / 0.108 ms |
| Transport events | 5,952 accepted, 0 dropped |
| Go retained heap / goroutines | 0 B / 0 additional |
| Extension events | 12,801 emitted, 2,411 delivered, 10,390 accounted drops |
| CDP queue high-water / delivery p95 | 32 events / 48 ms |
| Extension retained heap | 285,888 B |
