# Plan — Deduplicate the root payload across trace steps

Spec: `2026-09-07-trace-payload-dedup-spec.md` (post codex review)
Issue: #957
Branch: `worktree-trace-payload-dedup`

All work is in `internal/store/trace.go` + `internal/store/trace_test.go`, plus one
assertion widened in `internal/module/agent/monitor_test.go`. The producer
(`internal/module/agent/trace.go`) and the monitor handler are not touched.

One phase — schema, write and read must land together or the store is inconsistent.

## Task 1 — Schema + migration (TDD)

**Tests first** (spec cases 14, 15):
- Fresh DB has both columns.
- Current schema missing both → both added, rows intact, subsequent save→read works.
- Only one of the two present → the other is added (migration is per-column, not
  all-or-nothing).
- Migration re-run is a no-op.
- Genuine legacy schema still goes through the rebuild path and ends with both columns.
- **Regression**: a current-schema database must NOT take the rebuild path after this
  change. Assert on an observable rebuild side effect rather than on absence of error,
  so the test would actually fail if the new columns were added to the required lists.

**Then implement**:
- `createTraceChainsTable`: add `root_payload_json TEXT NOT NULL DEFAULT 'null'`.
- `createTraceStepsTable`: add `payload_is_root INTEGER NOT NULL DEFAULT 0`.
- In `migrateTraceDB`, after the existing create / rebuild steps, re-read `table_info`
  on the same pinned `conn` and `ALTER TABLE … ADD COLUMN` whichever is missing.
- **Do not** touch `needsChainRebuild` / `needsStepRebuild` required lists
  (`trace.go:186`) — adding the new columns there makes current-schema DBs take the
  rebuild path, whose chain copier reads `created_at` / `agent_type` that the current
  table lacks, failing migration. The regression test above guards this.

**Done when**: `go test ./internal/store/...` green; both migration paths produce both
columns.

## Task 2 — Write-side dedup (TDD)

**Tests first** (spec cases 1–8, 13, 16). Assertions read raw columns via SQL:
- identical payloads across trigger/verify/frame/projection → chain holds it, those steps
  have `payload_is_root = 1` and `payload_json = ''`, `emit` untouched
- all-distinct → nothing deduped, `root_payload_json = 'null'`
- `[A,B,B]` → nothing; `[A,A,B,B]` → only the A pair
- two-step trigger+verify chain dedups; single-step `probe-intent` chain does not and
  gains no chain-row copy
- semantically-equal-but-byte-different payloads keep their own copies
- steps supplied out of order pick the root the read path shows first
- `Seq == 0` defaults and equal-`Seq` tie-breaks resolve via `created_at` / `step_id`
- zero steps; nil / empty `RawMessage` / literal `null` / `{}` each stored per
  `rawJSONText`, none mistaken for the dedup marker
- re-save transitions: inline→dedup, dedup→inline, root A→B, steps shrinking to empty
- a failing step INSERT after the root was written leaves the previous chain intact

**Then implement** in `SaveChain`, after `normalizeTraceRecord`:
1. No sorting needed — `normalizeTraceRecord` already ends with a `sort.SliceStable` on
   `Seq → CreatedAt → StepID` (`trace.go:812`), matching the read query's
   `ORDER BY seq ASC, created_at ASC, step_id ASC` exactly. Do not re-sort.
2. `stored[i] = rawJSONText(steps[i].PayloadJSON)`.
3. Root candidate = `stored[0]` of the normalized (already sorted) slice; empty chain
   disables dedup.
4. If fewer than two steps share it → dedup off: `root_payload_json = 'null'`, all steps
   inline (today's behaviour).
5. Else → chain gets the candidate string; matching steps insert `payload_json = ''`
   with `payload_is_root = 1`, passing `''` as a literal SQL value (NOT through
   `rawJSONText`, which maps empty to `"null"`). Others stay inline with flag 0.
6. Add `root_payload_json` to the chain INSERT column list **and** the
   `ON CONFLICT DO UPDATE` set, inside the existing write transaction.

**Done when**: storage-shape tests green.

## Task 3 - Read-side rehydration (TDD)

**Tests first** (spec cases 9-12):
- `GetChainRecord` returns byte-identical `PayloadJSON` per step, asserted against the
  saved values. For nil / empty `RawMessage` inputs the expected read-back is `"null"`,
  not the original nil bytes.
- legacy rows inserted directly via SQL read back unchanged
- read->save round trip is stable and does not mutate the caller's payload slices
- **Read/write interleaving (case 12), via a deterministic seam** - a goroutine loop or
  `-race` cannot reliably reproduce "read root A, then read steps B". Extract the read
  body into an unexported helper taking the existing `sqlQuerier` (`trace.go:90`); the
  exported method passes `s.db`. The test's wrapper runs `SaveChain(B)` to completion
  *before* the step query executes, then delegates. Seed A, expect B's step IDs and
  payloads. Use the file-backed pool helper (`trace_test.go:764`); do NOT put the reader
  in a single-connection transaction and then wait on the writer, or the test deadlocks.
  **Assert only that steps and their payloads are the same version.** The chain summary
  still comes from a separate query - the JOIN does not make the whole `TraceRecord` one
  version, and the test must not claim it does.

**Then implement**:
- Rewrite the step query in `GetChainRecord` to JOIN the chain and resolve inline,
  keeping the existing 17 result columns in order with the CASE in the 14th (payload)
  position, so the positional scanner in `collectTraceSteps` needs no change:

```sql
SELECT s.step_id, s.chain_id, s.parent_step_id, s.seq, s.kind, s.tmux_session, s.pane_id,
       s.agent_type, s.frame_id, s.parent_frame_id, s.event_name, s.decision, s.reason,
       CASE WHEN s.payload_is_root = 1
            THEN c.root_payload_json ELSE s.payload_json END AS payload_json,
       s.before_json, s.after_json, s.created_at
FROM agent_trace_steps s
JOIN agent_trace_chains c ON c.chain_id = s.chain_id
WHERE s.chain_id = ?
ORDER BY s.seq ASC, s.created_at ASC, s.step_id ASC
```

- Qualify every shared column with `s.` / `c.` - especially `chain_id`, `tmux_session`,
  `pane_id`.
- Do not select the root/flag separately and do not change `TraceStep`.
- `collectTraceSteps` has exactly one caller (`trace.go:735`, scanner at `:1011`), and it
  scans positionally, so this rewrite needs no scanner change - confirm before relying on
  it.
- Run `EXPLAIN QUERY PLAN` once under the project's modernc driver to confirm the JOIN
  uses `idx_trace_steps_chain_seq` (`trace.go:352`) with no extra sort B-tree. Do **not**
  pin the EQP output string as a test assertion.

**Done when**: read-back tests green; save->read round trip byte-identical to pre-change
behaviour.

## Task 4 - API contract, restart safety, verification

**API contract (spec case 18).** Widening the assertion is not enough: the fixture at
`monitor_test.go:104` uses three distinct payloads and so never triggers dedup. Change
trigger/verify to share one payload, keep `emit` distinct. Capture the expected response
bytes **before** the writer changes land, then compare `w.Body.Bytes()` directly
(including the encoder's trailing newline). Do not generate the expected value from the
post-change `GetChainRecord` or the production DTO builder - they would carry the same
bug into the expectation.

**Restart regression (file-backed).** Save a deduped chain, close the store, reopen it so
`Traces()` re-runs migration, then verify the raw root/flags and the rehydrated payload.
Migration-rerun on an empty DB does not prove that already-deduped data survives a
restart.

**Pruning (spec case 17).** Mixed deduped + legacy chains leave no orphans and survivors
still rehydrate. When seeding legacy rows directly, also set the chain's `step_count` -
pruning counts from the chain row (`trace.go:881`) - and exercise the chain cap and the
step cap separately.

**Verification commands** (all must be run, with output shown):
- `go test ./...`
- `make test` (Makefile:11 - the whole suite with `-race -count=1`)
- `go test ./internal/store/... -race -count=2`
- `go vet ./...`, `go build ./...`
- `gofmt -l internal/store/trace.go internal/store/trace_test.go internal/module/agent/monitor_test.go`
  (bare `gofmt -l` with no path reads stdin and proves nothing)

## Release and rollback constraint

This changes the on-disk format, so it carries a constraint no previous change in this
area had:

**An older daemon must not be pointed at a database that has been written by this
version.** The old reader only reads `payload_json`, so it would read deduped rows as
empty payloads - silently, with no error. Deployment must not have old and new binaries
sharing one database, and a rollback must either use a build that still understands the
new format, or restore a pre-upgrade backup while accepting the data loss back to that
point in time.

No automatic downgrade path is being added; the constraint is documented and that is the
mitigation. It must also be stated in the PR description, because whoever deploys or
rolls back is the person who needs it.

## Risks

| Risk | Handling |
|---|---|
| New columns added to the rebuild required lists break migration | Task 1 asserts `needsChainRebuild` / `needsStepRebuild` return false for current schema + new columns |
| Read pairs a new root with old steps | Task 3 JOIN makes it one snapshot; deterministic seam test proves it |
| `''` marker collides with a real empty payload | `rawJSONText` never returns `''` (empty -> `"null"`), so `''` is unreachable for a genuine value; pinned by a test |
| Re-save leaves a stale root | Root recomputed and written on every upsert, including reset to `'null'`; transition tests cover it |
| Old binary reads a deduped database | Documented release constraint above; stated in the PR |
| Dedup rate lower than the measured `DISTINCT` figure | Accepted: byte equality only. Correctness never depends on the rate |

## Out of scope

Payload truncation and retention changes (spec Non-goals). Existing rows are not
backfilled - they are replaced by attrition as `pruneTraceChains` evicts under load.

PR, two review rounds, merge, a separate bump PR and deploy verification all follow the
existing project process and are not restated as tasks here.
