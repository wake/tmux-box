# Spec — Deduplicate the root payload across trace steps

Date: 2026-09-07
Branch: `worktree-trace-payload-dedup`
Issue: #957
Reviewed by: codex (spec review round 1) — findings folded in below

## Problem

`agent_trace_steps` stores three JSON blobs per row. Measured on the live database
(10,000 chains / 40,363 steps / 389 MB after VACUUM):

| column | total | avg/row | max |
|---|---|---|---|
| `payload_json` | **313.9 MB** | 8.2 KB | **682 KB** |
| `after_json` | 4.8 MB | 126 B | 2.4 KB |
| `before_json` | 2.4 MB | 62 B | 1.6 KB |

`payload_json` is effectively the whole database, and **73% of it is duplication**:

```
sum(length(payload_json))                    -> 313.9 MB
sum over DISTINCT (chain_id, payload_json)   ->  83.3 MB
```

The cause is in `internal/module/agent/trace.go`: the `trigger`, `verify`, `frame` and
`projection` steps each pass the same `req` (the whole `EventRequest`) as their payload.
Confirmed on the largest chain — four byte-identical 682,205-byte rows:

```
af807e49… | trigger    | 682205 | {"tmux_session":"ai-chat",…
af807e49… | verify     | 682205 | {"tmux_session":"ai-chat",…
af807e49… | frame      | 682205 | {"tmux_session":"ai-chat",…
af807e49… | projection | 682205 | {"tmux_session":"ai-chat",…
af807e49… | emit       |    152 | {"agent_type":"cc",…          <- genuinely different
```

`PdxPostToolUse` is the expensive event because its payload carries the tool result,
which can be a whole file's contents.

Note on write cost: the collector's `append` only accumulates in memory; `Finish`
enqueues once and the sink worker calls `SaveChain` once per chain
(`internal/module/agent/trace.go:267`, `:411`, `:107`). So this is not per-step write
amplification — a chain is written once, with the payload repeated inside that one
write. The saving is in stored and written bytes, not in the number of writes.

## Goals

Store the shared root payload once per chain. Nothing observable changes: the monitor
API returns byte-identical JSON.

## Non-goals

- **Payload size cap / truncation.** 768 rows (1.9%) exceed 64 KB and account for
  241 MB (77%) of payload bytes, so a cap would save more than dedup — but it is lossy,
  and dedup is not. Ship dedup first and measure. Tracked in #957.
- **Retention changes.** `pruneTraceChains` caps at 10,000 chains / 100,000 steps.
  Explicitly unchanged.
- Backfill of existing rows (see Migration).

## Design

Dedup lives entirely in the store layer (`internal/store/trace.go`). The producer is not
touched — lower risk, and the redundancy is a storage concern.

### Schema

- `agent_trace_chains` gains `root_payload_json TEXT NOT NULL DEFAULT 'null'`.
- `agent_trace_steps` gains `payload_is_root INTEGER NOT NULL DEFAULT 0`.

A separate flag column (rather than a NULL / sentinel inside `payload_json`) keeps
"payload is literally JSON `null`" distinguishable from "payload equals the chain root",
and leaves the existing `NOT NULL DEFAULT 'null'` constraint untouched. Both columns are
non-FK, non-UNIQUE, with constant non-NULL defaults, so SQLite's `ALTER TABLE ADD COLUMN`
accepts them.

### Write (`SaveChain`)

Operates on the output of `normalizeTraceRecord`, which fills defaults (including
`Seq == 0`) and ends with a `sort.SliceStable` on `Seq → CreatedAt → StepID`
(`internal/store/trace.go:812`) — exactly the comparator the read query's
`ORDER BY seq ASC, created_at ASC, step_id ASC` uses.

1. No re-sorting is needed: the normalized slice is already in read order. `seq` is not
   unique, which is why those tie-breaks exist and matter.
2. The root candidate is therefore simply `steps[0]`. An empty chain disables dedup.
3. Compute the **stored form** of every payload via `rawJSONText`
   (`internal/store/trace.go:1066`), which maps empty/nil to the string `"null"`.
   Compare those strings, not the raw `json.RawMessage`.
4. If fewer than two steps share the candidate's stored form, dedup is off: write
   `root_payload_json = 'null'` and every step inline, exactly as today.
5. Otherwise write the candidate string to `agent_trace_chains.root_payload_json`, and
   for each step whose stored form matches it, insert `payload_json = ''` with
   `payload_is_root = 1`. The `''` must be passed as a literal SQL value — it must not
   go through `rawJSONText`, which would turn it into `"null"`. Non-matching steps keep
   their inline payload with `payload_is_root = 0`.

`root_payload_json` is recomputed and rewritten on **every** upsert — including resetting
it to `'null'` when dedup turns off, switching root A→B, and chains whose steps shrink or
empty. It must be in both the INSERT column list and the `ON CONFLICT DO UPDATE` set, and
it must stay inside the existing write transaction alongside the step rows.

**Scope of the algorithm.** Only the root payload is deduped. `[A, B, B]` dedups nothing;
`[A, A, B, B]` dedups only the A pair. Real hook chains are `[A, A, A, A, B]` (four
copies of `req` plus a distinct `emit`), which this fully collapses — but the 83.3 MB
`DISTINCT` figure above is a measurement of the data, not a guarantee of this algorithm.

### Read (`GetChainRecord`)

`GetChainRecord` (`internal/store/trace.go:704`) is the **only** path that returns steps:
`ListChains` returns summaries without steps, and the projection endpoint reads no step
payloads. `handleMonitorChain` (`internal/module/agent/monitor.go:100`) is its only
caller. So rehydration has exactly one site.

Rows with `payload_is_root = 1` get `PayloadJSON` from the chain's `root_payload_json`.
If the stored root is `"null"`, rehydration yields `"null"` — preserving today's boundary
value for an empty payload.

**Snapshot consistency.** `GetChainRecord` currently issues two unsynchronized queries
(chain at `:706`, steps at `:735`). Once the chain row carries the payload the steps
depend on, an interleaved `SaveChain` for the same chain could pair root B's chain row
with root A's step rows, silently returning the wrong payload. Fix by making the step
query `JOIN` the chain and resolve inline:

```sql
CASE WHEN s.payload_is_root = 1 THEN c.root_payload_json ELSE s.payload_json END
```

so flag and payload always come from one snapshot. (A shared read transaction around
both queries is an acceptable alternative; the JOIN is preferred because it makes the
invariant structural rather than procedural.)

### Byte comparison, precisely

The rule is: **merge only payloads whose stored bytes are equal.** Semantically equal but
byte-different payloads keep their own copies — that lowers the dedup rate and never
affects correctness. This is exactly what a byte-identical API contract requires.

`encoding/json` sorts map keys, so ordinary map iteration order is not a source of
nondeterminism here, and repeated marshalling of an unchanged `EventRequest` is stable.
But that reasoning must not be generalized: a custom `MarshalJSON` can be
nondeterministic, and object key order inside a `json.RawMessage` is passed through
untouched. The byte rule holds regardless.

Two different empty-value conventions exist and must not be conflated in tests:
`marshalTraceJSON(nil)` returns `{}` (`internal/module/agent/trace.go:426`), while the
store's `rawJSONText` maps an empty `RawMessage` to `"null"`.

### Migration

`migrateTraceDB` adds each missing column via `ALTER TABLE … ADD COLUMN`, on the same
pinned connection, **after** the existing create / legacy-rebuild steps, by re-reading
`table_info` and filling whatever is absent. Running with only one of the two columns
already present must still complete.

**The new columns must NOT be added to the `needsChainRebuild` / `needsStepRebuild`
required lists** (`internal/store/trace.go:186`). Doing so would make every current-schema
database take the legacy rebuild path, where the chain copier reads `created_at` /
`agent_type` — columns the current table does not have — and migration would fail.

The rebuild path's `CREATE TABLE` statements do include the new columns (they share the
`createTrace*Table` helpers), and its `INSERT … SELECT` names columns explicitly, so
defaulted columns need no copier change. A rebuild of an already-deduped table would,
however, only copy inline payloads — noted as a constraint should the copier ever be
reused.

Existing rows keep `payload_is_root = 0` and their inline payloads, so they read back
unchanged with no backfill. Correctness must not depend on whether `root_payload_json` is
`'null'` — only on the per-step flag.

## Expected effect

Deduped payload bytes: 313.9 MB → 83.3 MB of live data (measured `DISTINCT`; the
algorithm reaches this for the real `[A,A,A,A,B]` chain shape).

Two caveats on how that shows up:

- **The old rows are replaced by attrition, not by a TTL.** `pruneTraceChains` runs only
  inside `SaveChain` and only evicts when a cap is exceeded
  (`internal/store/trace.go:660`, `:910`). At the traffic level that produced the current
  window, 10,000 chains span ~32 hours, so replacement takes roughly that long — but if
  traffic drops, old rows persist much longer.
- **Deleting rows frees pages, it does not shrink the file.** The database is not opened
  with FULL auto-vacuum (`internal/store/agent_event.go:40`), so freed space becomes
  reusable pages. Live-data size, free pages, and on-disk file size are three different
  numbers; the post-change file size is an estimate pending measurement, and a `VACUUM`
  is what actually returns space to the filesystem.

## Test plan (TDD — tests first)

Assertions on storage shape read the raw columns via SQL, so storage is pinned
independently of the read path. Use the file-backed pool helper
(`trace_test.go:837`) where connection-pool behaviour matters, not only in-memory DBs.

**Dedup decision**
1. Identical payloads across trigger/verify/frame/projection → chain holds the payload,
   those steps have `payload_is_root = 1` and `payload_json = ''`, `emit` untouched.
2. All-distinct payloads → nothing deduped, `root_payload_json = 'null'`.
3. Partial dedup — `[A,B,B]` dedups nothing; `[A,A,B,B]` dedups only the A pair.
4. Two-step chain (trigger + verify, the `verify rejected` shape) dedups normally; a
   genuine single-step chain (`probe-intent`) does not, and gains no chain-row copy.
5. Byte fidelity — payloads that are semantically equal but differ in key order or
   whitespace keep their own bytes.

**Ordering and empty values**
6. Steps supplied out of order still pick the root the read path shows first.
7. `Seq == 0` defaults and equal-`Seq` tie-breaks resolve via `created_at` / `step_id`.
8. Zero steps; nil `PayloadJSON`; empty `RawMessage`; literal `null`; `{}` — each stored
   and read back per `rawJSONText` semantics, and none mistaken for a dedup marker.

**Read**
9. `GetChainRecord` returns byte-identical `PayloadJSON` for every step, asserted against
   the values that were saved, not against deduped storage.
10. Legacy rows (`payload_is_root = 0`, inline payload, `root_payload_json = 'null'`)
    inserted directly via SQL read back unchanged.
11. Read→save round trip — re-saving a `GetChainRecord` result stores the same shape and
    reads back the same, and saving does not mutate the caller's payload slices.
12. Read/write interleaving — with root switching A→B, a read never pairs A's payload
    with B's steps.

**Re-save transitions**
13. inline→dedup, dedup→inline, root A→B, and steps shrinking to empty each leave a
    correct `root_payload_json` and correct per-step flags.

**Migration matrix**
14. Fresh DB; current schema missing both columns; only one of the two present;
    migration re-run; genuine legacy schema through the rebuild path. Each ends with both
    columns present, rows intact, and a subsequent save→read working.
15. Regression: current-schema databases must **not** take the rebuild path after this
    change.

**Atomicity and pruning**
16. A step INSERT failing after the chain root was written leaves the previous chain
    intact (write transaction).
17. Pruning a mixture of deduped and legacy chains leaves no orphans, and survivors still
    rehydrate.

**API contract**
18. With fixed IDs and timestamps, the full `GET /api/agent/monitor/chains/{id}` response
    bytes are unchanged for a deduped chain — every step's payload, not just the root's.
    (`monitor_test.go:158` currently only checks the root payload and child IDs/kinds.)

## Acceptance

- `go test ./...` passes; `go test ./internal/store/... -race -count=2` passes
- `go vet ./...` clean, `go build ./...` succeeds, `gofmt` clean
- Monitor API response for a chain is byte-identical before and after the change
