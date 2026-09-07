# Spec — `pdx start` health-check window

Date: 2026-09-07
Branch: `worktree-daemon-health-window`

## Problem

`pdx start` spawns `pdx serve` as a child, then probes `/api/health` for a fixed
`500ms + 5 × 200ms ≈ 1.5s`. If the probe does not succeed inside that window the
parent SIGKILLs the child and exits 1 (`cmd/pdx/daemon.go:136-158`).

On 2026-09-07 this window caused a real outage on mlab: `~/.config/pdx/agent_events.db`
had grown to 1.1 GB, so on a cold boot (file not in page cache) SQLite open alone
exceeded 1.5s. The daemon was killed by its own start command, leaving only a
`pdx.pid` pointing at a dead pid. There was no panic and no crash report, because
the process was SIGKILLed — making the failure look like a silent no-start.

The window is a fixed budget that has nothing to do with how long startup actually
takes. It gets tighter as the database grows, so it will recur.

## Goals

1. A slow-but-healthy startup must not be killed.
2. A child that genuinely dies must be reported promptly — not after the full window.
3. The failure message must say what actually happened.

## Non-goals

- Database size / retention for `agent_trace_steps` (tracked separately).
- Killing the child's whole process group instead of just the child (tracked separately).
- A configurable `--timeout` flag.

## Design

Extract the probe loop into a testable function:

```go
// waitForHealthy polls healthURL every interval until it returns 200, the child
// exits, or timeout elapses.
func waitForHealthy(healthURL string, childExited <-chan error, timeout, interval time.Duration, notify func(string)) error
```

- `timeout` = 60s (const `healthCheckTimeout`), `interval` = 200ms (const `healthCheckInterval`).
- `childExited` is closed/fed by a goroutine running `cmd.Wait()`. When it fires,
  `waitForHealthy` returns immediately with an error naming the child's exit status
  instead of waiting out the remaining window.
- `notify` is called once, after `healthCheckNotifyAfter` (3s), so a long wait prints
  a progress line rather than looking hung.
- Returns `nil` on the first 200.

`runStart` keeps its existing behaviour on failure: SIGKILL the child (harmless and
ignored if it already exited), tail the last 20 log lines, exit 1. The stderr message
distinguishes the two failure modes:

- child exited early → `pdx: daemon exited during startup (%v)`
- window elapsed → `pdx: daemon did not become healthy within %s`

## Test plan (TDD — tests first)

`cmd/pdx/daemon_test.go`, driving `waitForHealthy` against `httptest` servers:

1. **healthy immediately** — returns nil, well under timeout.
2. **healthy after several probes** — server 503s N times then 200s; returns nil.
3. **never healthy** — server always 503; returns a timeout error at ~timeout, not earlier.
4. **child exits early** — `childExited` fires while the server is still 503;
   returns a non-timeout error promptly (asserted well under the timeout).
5. **notify fires on a slow wait** — `notify` called exactly once when the wait
   exceeds the notify threshold, and not called on the fast path.

Tests use short injected durations (ms) so the suite stays fast.

## Acceptance

- `go test ./cmd/pdx/...` passes.
- `go vet ./...` clean.
- `go build ./...` succeeds.
- A `pdx start` against a cold 1.1 GB database no longer self-kills (verified by
  reasoning from case 2 + the measured cold-open cost; not reproducible on demand
  now that the db is 389 MB).
