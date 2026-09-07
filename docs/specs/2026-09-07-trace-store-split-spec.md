# Spec — Split the trace store by responsibility

Date: 2026-09-07
Branch: `worktree-trace-store-split`
Issues: #963 (source split), #964 (test split), #965 (dedup plan return type)

## Problem

After the payload-dedup change (#962), `internal/store/trace.go` is 1,195 lines and
carries eight distinct responsibilities in one file: type definitions, schema DDL,
migration, legacy rebuild, CRUD, retention/pruning, dedup planning, cursor encoding, and
JSON helpers.

Its tests are split across `trace_test.go` (953 lines) and `trace_dedup_test.go`
(1,310 lines), but the seam between them is chronological rather than topical: both
files cover migration and both cover pruning, and the newer file depends on fixtures
defined in the older one.

Raised by codex file-health review on #962, which found no mis-wiring — the concern is
growth and navigability, not a defect.

## Goals

Pure refactor. No behaviour change, no test semantics change. Every existing test keeps
its name and its assertions; tests only move between files.

## Non-goals

- Any change to what the store does, including the dedup algorithm's decisions.
- New abstractions: no service layer, no interfaces, no package split. Everything stays
  in `package store`, unexported identifiers stay unexported.
- Payload truncation, retention tuning (#967).

## Design

### Source layout (#963)

| File | Contents |
|---|---|
| `trace.go` | Types (`TraceChain`, `TraceStep`, `TraceRecord`, `TraceListFilter`, `TraceChainPage`, `TraceStore`), the `sqlQuerier` interface, and the retention constants |
| `trace_migration.go` | `migrateTraceDB`, `addMissingTraceDedupColumns`, `tableColumns`, `needsChainRebuild`, `needsStepRebuild`, `hasStepParentCompositeFK`, `createTraceChainsTable`, `createTraceStepsTable`, `createTraceIndexes`, `rebuildLegacyTraceChains`, `rebuildLegacyTraceSteps`, `legacyTraceStepCounts`, `traceTableExists`, `traceTableRowCount`, `legacyTraceOrphanStepCount` |
| `trace_write.go` | `SaveChain`, `normalizeTraceRecord`, `planTraceRootDedup`, `pruneTraceChains`, `traceLimits`, `rawJSONText` |
| `trace_read.go` | `ListChains`, `GetChainRecord`, `getTraceChainRecord`, `buildTraceChainListQuery`, `collectTraceChains`, `collectTraceSteps`, `encodeTraceCursor`, `decodeTraceCursor` |

`SaveChain`'s single write transaction — which spans the chain upsert, the step inserts
and `pruneTraceChains` — must remain one function in one file. Pruning lives with the
write path for that reason, not with migration.

`rawJSONText` goes with the write path because that is where it is called (three sites in
`SaveChain`, one in `planTraceRootDedup`); the read path does not use it.

### Dead code

`firstNonEmpty` (`trace.go:1188`) has no callers anywhere in the repository, tests
included. Delete it rather than carrying it into a new file.

### Test layout (#964)

| File | Contents |
|---|---|
| `trace_test_helpers_test.go` | Only fixtures used by more than one file: `openTestTraceStore`, `openFileTraceStore`, and the raw-shape readers (`rawTraceStep`, `readRawTraceSteps`, `assertRawShape`, `dedupStep`, `dedupRecord`) |
| `trace_migration_test.go` | All `TestTraceStore_MigratesLegacy*`, all `TestMigrateTraceDB_*` (both files), `TestNeedsChainRebuild_*` / `TestNeedsStepRebuild_*`, `TestTraceStore_DedupedChainSurvivesRestart` |
| `trace_retention_test.go` | `TestTraceStore_Retention*`, `TestTraceStore_PruneCascadesStepsToChainsAfterEviction`, `TestTraceStore_PruneMixedDedupedAndLegacyChains` |
| `trace_test.go` | `TestTraceStore_SaveAndGetChainRecord`, `TestTraceStore_ListChains_PaginatesWithCursorAndBefore`, `TestTraceStore_RejectsCrossChainParentStep` |
| `trace_dedup_test.go` | `TestSaveChain_*` dedup cases and `TestGetChainRecord_*` |

Restart sits with migration for a sharper reason than "the database has rows": that test
closes and reopens a file-backed database so `Traces()` re-runs migration, then checks the
schema, the raw stored values and the rehydrated payload — it is the case that pins
"re-running migration must not damage already-deduped data".

Single-topic fixtures live with the topic that uses them, verified by grepping callers:
the `seedLegacy*` / `seedIntermediate*` / `seedPreDedup*` schema builders and the column
assertions are migration-only; `seedLegacyDedupFreeChain`, `saveDedupedChain`,
`assertNoOrphanSteps` and `assertMixedSurvivorsRehydrate` are retention-only;
`dedupTestStore`, `readRawTraceRoot` and `interleavingQuerier` are dedup-only. Only
genuinely shared helpers are centralized — grouping by name prefix would have been the
cruder rule (codex spec review).

### `planTraceRootDedup` return type (#965)

Currently returns `(string, []bool)` where the bool slice is positionally aligned with
the caller's `steps` — an implicit coupling — and `SaveChain` then calls `rawJSONText` a
second time per step to build the value it stores.

```go
type storedTracePayload struct {
    Payload string // the stored form; "" when IsRoot
    IsRoot  bool
}

type tracePayloadPlan struct {
    RootPayload string
    Steps       []storedTracePayload
}
```

`SaveChain` writes `plan.Steps[i].Payload` and `plan.Steps[i].IsRoot` directly. This
removes the duplicate `rawJSONText` pass and binds each payload to its flag, so the
"deduped steps store the empty string" rule becomes a property of the plan rather than a
branch in the insert loop.

**It does not remove the positional coupling** (codex spec review): `SaveChain` still
pairs `steps[i]` with `plan.Steps[i]`. The contract is therefore explicit:

- The planner takes steps that `normalizeTraceRecord` has already filled in and sorted,
  and returns a slice of the same length in the same order. Neither side may be reordered
  independently afterwards.
- Every `Payload` is the `rawJSONText` result. With zero steps, or fewer than two
  matching the candidate, `RootPayload` must be `"null"` — never the struct's zero
  value `""`.
- The candidate is captured and the matches counted *before* any entry is blanked, so the
  comparison basis is never the already-emptied first entry.

The empty string for a deduped step reaches SQL as a bound parameter value — the plan
carries it as `""` and `SaveChain` passes it straight to `tx.Exec`. It must not be routed
back through `rawJSONText` (which would expand it to `"null"`), and it must not become
string concatenation into the SQL text. The existing raw-storage tests assert
`payload_json == ""` with the flag set, which is what catches a regression here;
asserting only on read-back would not.

## Verification

The refactor is correct if behaviour is provably unchanged:

- **Declaration-level comparison.** Every top-level declaration is extracted by name from
  the files at `HEAD` and from the working tree, and the bodies compared. Everything must
  be byte-identical except an explicit allow-list: `firstNonEmpty` (deleted),
  `planTraceRootDedup` and `SaveChain` (rewritten for #965), and the two new types. A
  matching line count would not prove this — SQL, an assertion or a condition could change
  while the totals stay level — so the comparison is on content, not on a diffstat.
- **Test name list**, captured with `go test ./internal/store/... -list '^Test'` before and
  after, sorted and diffed. This covers top-level names only; subtests and assertions are
  covered by the declaration comparison above.
- `go test ./... -count=1` (cache disabled) passes.
- `go test ./internal/store/... -race -count=2` passes.
- `go vet ./...`, `go build ./...` pass; `gofmt -l` clean for the files touched.
- Manual review that SQL/DDL text, result-column order, `ORDER BY` clauses and transaction
  boundaries are unchanged — the read path scans positionally, so column order is load
  bearing.
