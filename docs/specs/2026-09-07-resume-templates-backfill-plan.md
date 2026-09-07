# Resume Templates & Provenance Backfill — Implementation Plan

**Status:** v5 — approved to start by codex plan review `task-mtriamoh-lqcjyb` (0 Blocker, 0 Important, 2 Minor, both folded in). Revised after codex plan reviews `task-mtrhhdht-bl382f` (1 Blocker, 11 Important, 1 Minor) and `task-mtrhv4kc-507pse` (2 Blocker, 2 Important) and `task-mtri4jku-kvcoe5` (1 Blocker, 1 Important, 1 Minor); dispositions at the end.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Two follow-ups to Tab Rebuild. **(B)** A tmux session that was already
running when alpha.332 shipped currently rebuilds as a bare shell, because only
a `SessionStart` ever wrote its agent — the SPA learns to *ask* the daemon who
owns a pane. **(A)** The composed `claude --resume <id>` calls the wrong program
for a user who launches Claude Code through a shell function — per-agent
templates in Settings, with a save-time check that the command actually
resolves.

**Architecture:** The daemon already decides pane ownership in the frame layer
and already receives session ids on every hook event; neither is stored. Phase 1
puts the sender's session id and cwd on its own frame row, through a dedicated
narrow UPDATE that never rides a read-modify-write. Phase 2 turns
`classifyAncestor`'s traversal into a shared walker that answers both "is this
frame inside the pane's process tree" and "is anything framed above it", and
exposes the result as `GET /api/sessions/{code}/provenance`. Phase 3 has the SPA
ask — under the same generation rules as the existing cwd probe — and write the
answer under an ordered four-mode policy that terminates. Phases 4–6 replace the
hardcoded resume commands with templates, a per-pane override and a shell probe.

**Tech Stack:** Go (net/http, modernc.org/sqlite, gorilla/websocket) · React 19
/ Zustand 5 / Vitest / Tailwind 4 · pnpm

**Spec:** `docs/specs/2026-09-07-resume-templates-backfill-spec.md` (v5)

## Global Constraints

- **TDD, no exceptions.** Every task writes the failing test first, runs it to
  see it fail, then implements. Each task is one commit.
- **Commit messages in English**; conversation replies in Traditional Chinese
  (project convention). Every commit ends with:
  ```
  Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01CgHRHATNTPzwCEU1avaYjV
  ```
- **Verification commands** (the root `package.json` has no `lint`/`build`
  scripts — these exact forms are required):
  ```
  go test ./...
  pnpm --prefix spa exec vitest run
  pnpm --prefix spa run lint
  pnpm --prefix spa run build
  ```
- **No persist migration.** Alpha convention. A `resumeCommand` key left in
  persisted pane state after Phase 4 is inert and must stay inert — no cleanup
  code, no reader.
- **Never widen an existing behaviour silently.** Task 5 is a pure refactor: if
  any existing test in `internal/module/agent` needs editing to pass, **stop and
  report** instead of editing it. The rule protects tests that predate this
  plan. A test written by an *earlier task of this same plan* whose invariant a
  later task deliberately changes (Task 4 → 4b) is a normal edit — say so in the
  commit message. The same rule applies to
  `internal/module/agent/provenance_test.go` for the whole of Phase 1–2 — this
  work adds no envelope, so every assertion there, including
  `TestProvenance_NonSessionStart_NoEnvelope`, must stay green untouched.
- **i18n**: every user-visible string goes in BOTH `spa/src/locales/en.json`
  and `spa/src/locales/zh-TW.json`. Keys are flat dotted strings, e.g.
  `"resume_template.test"`.
- **Stream-mode panes (`mode: 'stream'`) are out of scope** in every task. Guard
  on `content.mode === 'terminal'` wherever panes are selected.
- **Go test packages are mixed — check before copying a snippet.** The
  `internal/agent/{cc,codex,opencode}` status tests are *external*
  (`package cc_test` etc.) and cannot call unexported functions; go through
  `Provider.DeriveStatus` via each file's existing helper (`deriveViaProvider`
  in cc/opencode, `deriveWithRaw` in codex). `internal/module/agent`,
  `internal/module/session` and `internal/store` tests are *internal* and may
  call unexported symbols directly. The opencode plugin-template tests are also
  internal (`package opencode`). **`internal/agent` itself is both** —
  `lifecycle_test.go` is `package agent`, `provider_test.go` is
  `package agent_test`. Anything that must import cc/codex/opencode has to live
  in the external file, since those packages import `agent` (Task 2 hit this).
- **`gofmt -l` is not clean at baseline** in `internal/agent/`
  (`cc/readiness_test.go`, `cc/statusline.go`,
  `codex/probe_intent_screen_change_test.go`, `opencode/events_test.go`,
  `opencode/status.go`) nor in `internal/module/agent/` (`fakes_test.go`,
  `frame_ops_test.go`, `handler_test.go`, `frame_ops_l2_test.go`,
  `probe_intent_dispatcher.go`,
  `probe_intent_dispatcher_observability_test.go`,
  `probe_orchestrator_test.go`, `statusline_selftest.go`). Format your own
  files; do **not** add a repo-wide `gofmt` gate, and do not reformat those
  thirteen as a side effect of another task.
- **The two identity columns never travel inside a `Frame` round-trip write.**
  Spec §5.2 has the per-method table; Task 1 implements it and Task 3 depends on
  it. Any task that adds a column to `UpsertIfUnchanged`, `UpdateHookPath`,
  `UpdateHookPathAndResetSubagents` or `UpdateStatusAndLastSeen` is wrong.
- **Do not add memoization to `classifyAncestor`.** Task 6's memo is
  request-scoped and belongs to the query only.
  `provenance_test.go:170` deliberately makes the sender's 1st/2nd/3rd process
  read return *different* values to exercise the post-Upsert reconcile; a memo
  on the hook path would silently break that test's premise.
- **Every commit must type-check on its own.** `vitest` and `lint` do not catch
  a reference to a field that does not exist yet, so any task that touches
  `spa/` runs `pnpm --prefix spa run build` before committing. This is why the
  override field is *added* in Task 12 and the old one *removed* in Task 13,
  rather than swapped in one step (see those tasks).

---

## File Structure

**Daemon (Go)**

| File | Responsibility |
|---|---|
| `internal/store/frames.go` *(modify)* | Two columns, additive migration, `UpdateSessionIdentity`, scans |
| `internal/agent/provider.go` *(modify)* | `SessionIdentifier` optional interface |
| `internal/agent/{cc,codex,opencode}/provider.go` *(modify)* | `IdentifyEvent` per agent |
| `internal/agent/identity.go` *(new)* | Shared two-field extractor + its benchmark |
| `internal/module/agent/frame_ops.go` *(modify)* | Calls `UpdateSessionIdentity` after the frame mutation |
| `internal/module/agent/ancestor.go` *(modify)* | Traversal extracted into a shared walker |
| `internal/module/agent/pane_owner.go` *(new)* | Root resolution over a pane's frames, memoized reader |
| `internal/module/agent/provenance_handler.go` *(new)* | `GET /api/sessions/{code}/provenance` |
| `internal/tmux/{executor,fake_executor}.go` *(modify)* | `PaneSessionID` (Task 7) and `ShowGlobalOption` (Task 15) |
| `internal/agent/opencode/plugin_template.go` *(modify)* | `cwd` on `PdxStop` / `PdxUserPromptSubmit` |
| `internal/module/session/shell_resolve.go` *(new)* | `POST /api/shell/resolve-command` |


**SPA (TypeScript)**

| File | Responsibility |
|---|---|
| `spa/src/types/tab.ts` *(modify)* | `cwdSource` union, `agent-backfill` patch, `resumeCommandOverride` |
| `spa/src/stores/useTabStore.ts` *(modify)* | The four ordered backfill modes; `cwdSource: 'user'` |
| `spa/src/lib/rebuild/provenance-probe.ts` *(new)* | The request, the generation rules, the scheduler |
| `spa/src/lib/host-api.ts` *(modify)* | `fetchSessionProvenance`, `resolveShellCommand` |
| `spa/src/stores/useResumeTemplateStore.ts` *(new)* | Per-agent template pairs |
| `spa/src/lib/rebuild/composer.ts` *(modify)* | `resolveResumeCommand` replaces `composeResumeCommand` |
| `spa/src/components/settings/ResumeTemplateSettings.tsx` *(new)* | The template editor + Test button |
| `spa/src/components/RebuildActionSet.tsx` *(modify)* | Override field; operation-pinned display |
| `spa/src/components/{RenamePopover,TerminatedPane}.tsx` *(modify)* | Field rename |
| `spa/src/lib/rebuild/{batch,eligibility,engine}.ts` *(modify)* | Field rename; resolve at operation start |

---

# Phase 1 — The frame remembers its own identity (daemon)

### Task 1: Two columns, and the write contract that keeps them safe

**Files:**
- Modify: `internal/store/frames.go`
- Modify/Create: `internal/store/frames_test.go` (internal package)

**Interfaces produced:**
```go
// Frame gains:
SessionID string
Cwd       string

// New, the ONLY post-insert writer of those two columns:
func (s *FramesStore) UpdateSessionIdentity(frameID, sessionID, cwd string) error
```

**Context the implementer needs.** `Upsert` (`frames.go:124`) is
`INSERT … ON CONFLICT(pane_id, pid, process_start_time) DO UPDATE SET …`
(`frames.go:160-175`) followed by a re-`SELECT` through `GetByIdentity`
(`frames.go:179`). Add the two columns to the **INSERT column list only**; leave
them out of the `DO UPDATE SET` list. Because the method returns the re-selected
row, the returned struct then carries the stored value automatically — do **not**
add a zero-value merge for them alongside the ones at `frames.go:129-141`.

`UpdateSessionIdentity` writes each column only when its argument is non-empty,
so an event that carries a `session_id` but no `cwd` cannot blank a `cwd` a
previous event recorded. Two options are acceptable: `COALESCE(NULLIF(?, ''), col)`
in one statement, or building the SET list from the non-empty arguments. If both
arguments are empty it must be a no-op that runs no SQL. Returns `sql.ErrNoRows`
when the frame does not exist (a frame deleted between the mutation and this
call is normal, and Task 3 must tolerate it).

Migration, both halves: add the two columns to the `CREATE TABLE IF NOT EXISTS`
body **and** an additive `ALTER TABLE … ADD COLUMN` guarded by a
column-existence check, next to the existing `clearStaleSubagentsJSON` step
(`frames.go:62`). A fresh database then gets them from the start and the `ALTER`
fires only for a pre-existing table. Existing rows get `''`, which is correct —
the frame has not told us yet.

`UpdateSessionIdentity` with two empty arguments returns `nil`, not
`sql.ErrNoRows`: it ran no statement, so it has nothing to report — including
about whether the frame exists.

- [ ] **Step 1: Write the failing tests**

Cover, in `internal/store/frames_test.go`:
- a fresh `Upsert` persists both columns;
- a second `Upsert` for the same identity with **empty** `SessionID`/`Cwd`
  leaves the stored values intact, and the returned `Frame` carries them;
- a second `Upsert` with **non-empty** values also leaves the stored values
  intact (the `DO UPDATE SET` list omits them) and the returned `Frame` carries
  the *stored* values, not the arguments — this is the contract Task 3 relies on;
- `UpdateSessionIdentity` sets both; sets only the non-empty one; is a no-op for
  two empty arguments; returns `sql.ErrNoRows` for an unknown frame id;
- `UpsertIfUnchanged`, `UpdateHookPath`, `UpdateHookPathAndResetSubagents` and
  `UpdateStatusAndLastSeen` each leave both columns untouched — one test per
  method, each seeded with a known identity;
- `GetByIdentity`, `FindByPanePID`, `ListByPane` and `ListAll` all return them;
- migrating a database created without the columns adds them with `''` and
  preserves existing rows.

- [ ] **Step 2: Run the tests — see them fail**
- [ ] **Step 3: Implement**
- [ ] **Step 4: Run the tests — see them pass**
- [ ] **Step 5: Full suite** `go test ./...`
- [ ] **Step 6: Commit** — `feat(store): record each frame's agent session id and cwd`

---

### Task 2: The two-field identity extractor

**Files:**
- Create: `internal/agent/identity.go`, `internal/agent/identity_test.go`
- Modify: `internal/agent/provider.go`
- Modify: `internal/agent/{cc,codex,opencode}/provider.go` (+ their tests)

**Interfaces produced:**
```go
// internal/agent/provider.go — optional, discovered by type assertion.
type SessionIdentifier interface {
    IdentifyEvent(purdexName string, rawEvent json.RawMessage) (sessionID, cwd string)
}

// internal/agent/identity.go — the shared implementation the three agents call.
func ExtractSessionIdentity(rawEvent json.RawMessage) (sessionID, cwd string)
```

**Context.** All three agents put `session_id` and `cwd` at the top level of
`raw_event` (spec §3.1), so the extraction is shared and the per-agent method is
a one-line delegation — the interface exists so a future agent with a different
shape has somewhere to differ, not to justify three copies today.

**Decode into a typed struct, not a map.** cc `PostToolUse` payloads embed whole
tool inputs; `encoding/json` skips an unknown field's value without
materialising it, whereas `map[string]json.RawMessage` would *copy* every value.
**Any decode failure returns `("", "")`** — malformed JSON, and also a
*well-formed* payload whose `session_id` is not a string. `encoding/json` would
happily hand back the good `cwd` alongside an `UnmarshalTypeError` for the bad
`session_id`, and this deliberately does not: a payload whose identity field is
the wrong type is not one we understand, and recording half of it would put a
directory on a frame whose session we could not read. It is the same
"no evidence, no action" rule the rest of the feature runs on. Never returns an
error — an unparseable hook payload is not an error condition here.

- [ ] **Step 1: Write the failing tests**
  - both fields present; only `session_id`; only `cwd`; neither; malformed JSON;
    `null` literals; a payload with a large `tool_input` still yielding the
    right two values;
  - each of the three providers satisfies `SessionIdentifier` and returns the
    same values for the same payload (external test packages — go through the
    provider value, not the unexported function);
  - **`BenchmarkExtractSessionIdentity`** over a payload with a ~256 KiB
    `tool_input`, run with `-benchmem`. Record the result in the commit message.
    The assertion the benchmark backs is *the unknown value is not
    materialised*: allocated bytes per op must not scale with the `tool_input`
    size (compare 4 KiB vs 256 KiB fixtures in the same benchmark file). It is
    **not** a zero-allocation claim — `encoding/json`'s scanner allocates.

- [ ] **Step 2: Run the tests — see them fail**
- [ ] **Step 3: Implement**
- [ ] **Step 4: Run the tests — see them pass**
- [ ] **Step 5:** `go test ./...` and `go test -bench=ExtractSessionIdentity -benchmem ./internal/agent/`
- [ ] **Step 6: Commit** — `feat(agent): extract the sender's session id from any hook payload`

---

### Task 3: Wire the extractor into the frame mutation

**Files:**
- Modify: `internal/module/agent/frame_ops.go`
- Modify: `internal/module/agent/frame_ops_test.go` or a new
  `identity_write_test.go` (internal package)

**Context.** In `applyFrameEvent`, after the frame mutation has produced
`stored`, call the provider's `IdentifyEvent` on `req.RawEvent` and, if either
value is non-empty, `m.frames.UpdateSessionIdentity(stored.FrameID, …)`.

Placement matters:
- It must run on the **ordinary-event path** — that is the whole point, since a
  pre-deploy session's frame only ever sees ordinary events, and those take
  `UpdateHookPath` (`frames.go:305`), not `Upsert`.
- It must **not** run on paths where `stored` is not the sender's own frame:
  the proxy-attach fast path and `reconcileCreatedFrameAsProxy` both return
  early with a *parent's* frame. Writing the child's session id onto the
  parent's row would be a real mis-attribution.
- **Every store error is logged and swallowed**, not only `sql.ErrNoRows`. The
  identity write happens *after* the frame mutation has landed, so the event's
  status semantics are already correct; failing the hook handler with a 500 over
  an auxiliary column would turn a cosmetic loss into a regression. Use distinct
  log keys for "frame gone" and "write failed" so the two are separable.
- Two own-frame returns are **deliberately excluded**: `derive_invalid` (a
  `/compact` `SessionStart` is rejected upstream and arrives here only as
  `Valid: false`), and `session_end` (the row has just been `Delete`d — spec
  §5.2 says the identity goes with the frame).
- A provider that does not implement `SessionIdentifier` skips the call.

**`applyFrameEvent` has several success returns and only the general one is
obvious. Wire all four own-frame returns, and none of the parent-frame ones:**

**The governing rule** — write the identity exactly when the frame being
returned is the frame of the process that sent the event. Judge every return by
that rule; the table below enumerates the own-frame ones (which is the list you
must be exhaustive about) and gives examples of the others.

| Own-frame return — **write** | |
|---|---|
| `frame_ops.go:169-176` — `subagent_id_missing`: a `SubagentStart`/`Stop` whose payload has no `agent_id` returns the sender's existing frame as `skipped`. It is still a real own-frame event carrying `session_id`, and cc marks it `Valid` (`cc/status.go:107-110`) | |
| `frame_ops.go:213-221` — native `SubagentStart`/`SubagentStop` membership change, after `mutateSubagentsWithRetry` | |
| `frame_ops.go:348-357` — `native_subagent_detached_on_stop_failure` (**native**, not proxy — the reason string says so) | |
| the general `created_frame` / `updated_frame` return (`frame_ops.go:911` as of `9e75ee2`; the surrounding `else` block holds `reconcileCreatedFrameAsProxy`, so find the return by its `decision`/`reason`, not by line) | |

Returns that hand back **someone else's** frame must not write. These are
examples, not an exhaustive list — apply the rule: the pre-Upsert proxy fast
path (`frame_ops.go:568` onward), `reconcileCreatedFrameAsProxy`
canonicalization (`frame_ops.go:860-871`), and the codex broker parent upsert
(`frame_ops.go:287-294`). Returns with no frame at all (`frame_missing`,
error paths) write nothing.

Hooking only the general return would still pass a naive test while silently
dropping the identity for every `Stop` / subagent event — spec §5.2 says *every*
own-frame event contributes.

- [ ] **Step 1: Write the failing tests**
  - an ordinary event (`PdxStop`) on an existing frame writes the id;
  - a second ordinary event carrying no id leaves it;
  - an ordinary event carrying a **different** id replaces it (opencode's
    in-process session switch, spec §3.3);
  - a `cwd` arriving on a later event than the id fills in;
  - a `SessionStart` with `source == "compact"` (rejected upstream by
    `deriveCCStatus`) writes nothing;
  - **each of the two proxy paths separately** writes nothing to the parent's
    frame — the pre-Upsert fast path and the post-Upsert canonicalization are
    different code, and one case cannot stand for both. Reuse the proxy fixtures
    in `frame_ops_test.go`;
  - a native `SubagentStart`, a `StopFailure` native detach, and a
    `SubagentStart` **with no `agent_id`** each write the id — the three
    non-obvious own-frame returns. The last one must also leave the subagent
    membership untouched;
  - the codex broker parent-upsert path writes nothing to the parent's frame;
  - interleaving: an identity write between a proxy attach's read and its
    `UpsertIfUnchanged` retry leaves both the merged subagents list and the new
    id intact.
- [ ] **Step 2: Run — fail**
- [ ] **Step 3: Implement**
- [ ] **Step 4: Run — pass**
- [ ] **Step 5:** `go test ./...`. `provenance_test.go` must be green **unedited**
  — but note that its fixtures' providers do **not** implement
  `SessionIdentifier`, so its staying green proves nothing about this wiring.
  Build the new tests on a fixture whose provider does implement it.
- [ ] **Step 6: Commit** — `feat(agent): record the session id from ordinary hook events`

---

### Task 4: OpenCode emits `cwd` on more than `SessionStart`

**Files:**
- Modify: `internal/agent/opencode/plugin_template.go` (lines ~145, ~177)
- Modify: `internal/agent/opencode/plugin_template_test.go` and
  `plugin_template_bun_integration_test.go` (internal package)

**Context.** Add `cwd: pdxCwd()` to the `PdxStop` and `PdxUserPromptSubmit`
emits. `pdxCwd()` already exists (line ~75) and already returns `''` rather than
throwing. **Do not touch the `parentID` child-session filter** — the two guards are at
`plugin_template.go:99-106` and `150-157` (line 47 is its comment and 97 the
enclosing case, not the guard). Spec v1 §9.3 states the filter is a precondition
of the ownership invariant for opencode.

- [ ] **Step 1: Write the failing tests** — the rendered template contains
  `cwd: pdxCwd()` in both emits; the Bun integration test asserts both emitted
  payloads carry a non-empty `cwd`, alongside the existing assertion that pins
  the `parentID` filter.
- [ ] **Step 2: Run — fail**
- [ ] **Step 3: Implement**
- [ ] **Step 4: Run — pass**
- [ ] **Step 5:** `go test ./...`
- [ ] **Step 6: Commit** — `feat(opencode): emit cwd on stop and prompt events`

---

### Task 4b: A subagent's prompt must not rename the pane's session

**Files:**
- Modify: `internal/agent/opencode/plugin_template.go` (the `chat.message`
  handler, ~line 168)
- Modify: `internal/agent/opencode/plugin_template_test.go` and
  `plugin_template_bun_integration_test.go`

**Why this exists — found while implementing Task 4, not in the original plan.**
The plugin gates child (subagent) sessions out of `session.created`,
`session.status`, `session.error` and `session.deleted` via the
`subagentSessions` map, but **`chat.message` is not gated**. A subagent's prompt
therefore emits `PdxUserPromptSubmit` carrying the *child's* `session_id`, and
because parent and child share one pane and one sender PID, Task 3's identity
write lands it on the parent's frame. The pane's recorded session id then flips
to a subagent's for as long as that subagent is working, and a Rebuild during
that window would send `opencode -s <subagent-session>` — resuming the wrong
conversation.

**The minimal fix, and why it is safe.** When `subagentSessions.has(input.sessionID)`,
emit the event **without** `session_id` and `cwd`; keep the event itself and
every other field. `ExtractSessionIdentity` writes only non-empty values, so the
parent's identity survives untouched.

Nothing downstream reads `session_id` off this event: `deriveOpenCodeStatus`
puts it in `Detail` only on its `PdxSessionStart` branch
(`opencode/status.go:15`), and no other consumer exists. So the running/idle
semantics of a subagent prompt are unchanged — which is deliberate. Do **not**
suppress the event outright; that would change the lights, which is out of scope.

**Known limit, state it in the test's comment:** the gate is the in-memory
`subagentSessions` map, so a plugin reload loses it and a child prompt arriving
afterwards is indistinguishable from a parent's. That degrades to today's
behaviour rather than to something worse, and `session.created` re-registers the
child if it is created again.

- [ ] **Step 1: Write the failing tests** — the rendered template gates
  `session_id`/`cwd` on the child map; the Bun integration test drives a
  `session.created` **with** a `parentID`, then a `chat.message` for that child,
  and asserts the emitted `PdxUserPromptSubmit` carries **no** `session_id` and
  **no** `cwd`; the same test drives a parent `chat.message` and asserts both
  are present. Keep the existing assertion that a parent lifecycle event is
  emitted exactly once.
- [ ] **Step 2: Run — fail** · [ ] **Step 3: Implement** · [ ] **Step 4: Run — pass**
- [ ] **Step 5:** `go test ./...`
- [ ] **Step 6: Commit** — `fix(opencode): keep a subagent prompt from renaming the pane's session`

---

### Task 4c: Keep the Go mirror of the plugin honest

**Files:**
- Modify: `internal/agent/opencode/plugin_template_contract_test.go`
  (`pluginSimState.simulateChatMessage`, ~line 203)

**Context.** That function is a hand-written Go mirror of the JS `chat.message`
handler — its own comment says "Mirror of the JS template change" — and the
contract test drives it as if it were the plugin. After Task 4b it no longer
mirrors: it still returns `session_id` for a child session, so the contract test
would keep passing if the JS gate were deleted. The Bun runtime test is the real
authority, but a mirror that is knowingly wrong about a correctness-critical
gate is worse than no mirror.

Model the gate: when the session id is in the simulated `subagentSessions` map,
omit `session_id`. `cwd` was never modelled here and stays unmodelled — that is
a pre-existing approximation, not something to fix in passing.

- [ ] **Step 1: Write the failing test** — a simulated child `chat.message`
  yields no `session_id`; a parent's still does.
- [ ] **Step 2: Run — fail** · [ ] **Step 3: Implement** · [ ] **Step 4: Run — pass**
- [ ] **Step 5:** `go test ./...`
- [ ] **Step 6: Commit** — `test(opencode): mirror the child-prompt gate in the contract sim`

---

# Phase 2 — The ownership query (daemon)

### Task 5: Extract the traversal from `classifyAncestor` (pure refactor)

**Files:**
- Modify: `internal/module/agent/ancestor.go`
- Modify: `internal/module/agent/ancestor_test.go`

**Interfaces produced:**
```go
// procReader is the seam: classifyAncestor passes readProcessInfoFn directly,
// Task 6 passes a request-scoped memoizing wrapper around it.
type procReader func(pid int) (agentpkg.ProcessInfo, error)

// ancestryResult is what one walk reports. Task 5 fills these two fields;
// Task 6 adds SawPanePID by extending this struct, not the signature.
type ancestryResult struct {
    Verdict AncestorVerdict
    Frame   *store.Frame   // set for SameTypeAbove and ProxyParent only
}

// walkPaneAncestry walks startPID's PPID chain, capped at proxyMaxDepth,
// applying the existing liveness + identity gating to each candidate frame.
func (m *Module) walkPaneAncestry(
    paneID string, startPID int, agentType string, read procReader,
) (ancestryResult, error)
```

Task 6 **modifies this file again** to add the pane-membership output. Returning
a struct now is what makes that additive instead of a second signature churn.

**Context.** This is a **pure refactor**. `classifyAncestor` becomes:

```go
func (m *Module) classifyAncestor(req EventRequest) (AncestorVerdict, *store.Frame, error) {
    if m.frames == nil { return VerdictIndeterminate, nil, nil }
    info, err := readProcessInfoFn(req.SenderPID)
    if err != nil { return VerdictIndeterminate, nil, nil }
    res, err := m.walkPaneAncestry(req.TmuxPaneID, info.PPID, req.AgentType, readProcessInfoFn)
    return res.Verdict, res.Frame, err
}
```

**Pass `readProcessInfoFn` here, never a memo.** `provenance_test.go:170`
deliberately makes the sender's successive reads return different values;
memoizing the hook path would break that test's premise while leaving it green
for the wrong reason.

Note the loop **starts from the PPID**, not from the PID — the existing code
reads the sender's own info first and then walks from `info.PPID` (around
`ancestor.go:49-56`; find it by the `ppid := info.PPID` assignment, not the
line). Preserve that exactly, along with the depth cap, the
`ppid <= 1` → `VerdictRoot` exit, the self-parent guard, the stale-frame
continue and the identity-unverifiable abort. Do not change behaviour, only
where it lives.

- [ ] **Step 1: Write the failing test** — a direct `walkPaneAncestry` test for
  each of the four verdicts, mirroring the existing `classifyAncestor` cases.
- [ ] **Step 2: Run — fail**
- [ ] **Step 3: Implement**
- [ ] **Step 4: Run — pass**
- [ ] **Step 5:** `go test ./...`. **Every existing test in
  `internal/module/agent` must pass unedited.** If one does not, stop and report.
- [ ] **Step 6: Commit** — `refactor(agent): extract the pane ancestry walk`

---

### Task 6: Root resolution for a pane, with pane-tree membership

**Files:**
- Create: `internal/module/agent/pane_owner.go`, `pane_owner_test.go`
- **Modify: `internal/module/agent/ancestor.go`** — `ancestryResult` gains
  `SawPanePID`, and `walkPaneAncestry` takes an options struct with an
  **explicit opt-in** for the pane check:

  ```go
  type ancestryOpts struct {
      PanePID    int
      CheckPane  bool   // when false, PanePID is never consulted
  }
  ```

  **Do not use `panePID == 0` as the "disabled" sentinel.** A PPID of `0` is
  representable and the reader does not exclude it (`process_info.go:41-50`);
  the existing loop only treats `ppid <= 1` as a *terminator*, so a `0` on the
  chain would be compared before that check and could spuriously set
  `SawPanePID`. `classifyAncestor` passes `CheckPane: false` and is provably
  unaffected.
- Modify: `internal/module/agent/ancestor_test.go` — assert the adapter's reads
  are unchanged in count and order.

**Interfaces produced:**
```go
type PaneOwner struct {
    FrameID    string   // REQUIRED: Task 7's deterministic tie-break sorts on it
    AgentType, SessionID, Cwd, TmuxPaneID string
    LastSeenAt int64
}

// resolvePaneOwners returns the root frames of one pane. read is shared with
// every other call in the same request.
func (m *Module) resolvePaneOwners(
    ctx context.Context, paneID string, read procReader,
) ([]PaneOwner, error)

func newMemoProcReader(base procReader) procReader
```

**Context — this task carries review round 4's finding 2, so read it carefully.**

For each frame in `frames.ListByPane(paneID)`, keep it only if it is alive
(`isPidAliveFn`) and its `processStartTime` matches (`processStartTimeFn`).
Then, for each survivor, decide two things **in one walk**:

- **inside the pane's tree** — `panePID` (from `resolvePanePIDFn(m.tmux, paneID)`,
  signature `func(tmux.Executor, string) (int, error)`) appears on the chain;
- **not a root** — another surviving frame of this pane appears on the chain.

**The rule, stated once:** a frame is kept **iff the walk ran to completion with
`VerdictRoot` and `SawPanePID` was set somewhere along the way.** Everything else
is excluded — never promoted.

`SawPanePID` is *observational*: it records that `panePID` appeared on the chain
and never terminates the walk or licenses a shortcut. Seeing the pane proves
membership; it proves nothing about whether a framed ancestor sits further up,
so it can never on its own justify keeping a frame. v2 of this plan had a row
that said a hit at the last allowed depth should be kept, and that was wrong for
exactly this reason.

The table below is by **termination reason**, over the walks that end with
`err == nil` — one row fires per walk, so within that scope the rows are
exhaustive and mutually exclusive. `SawPanePID` is an independent bit that may
be true or false in any of them.

Two paths end with a **non-nil error** and are outside the table: a
`FindByPanePID` failure (`ancestor.go:60`) and context cancellation. Both
exclude the frame and propagate to the handler, which answers `found: false`
(Task 7). They are never treated as a verdict.

| The walk ended because… | Verdict | Frame kept? |
|---|---|---|
| `ppid <= 1` reached | `Root` | **yes iff `SawPanePID`** |
| another surviving frame of this pane was found on the chain | `SameTypeAbove` / `ProxyParent` | no — not a root |
| the depth cap was exhausted | `Indeterminate` | no |
| a process read failed | `Indeterminate` | no |
| the self-parent guard fired (`ancestorInfo.PPID == ppid`) | `Indeterminate` | no |
| identity of a candidate frame was unverifiable | `Indeterminate` | no |

**Where `SawPanePID` is set — two places, both load-bearing:**

1. **Before the loop**, if `frame.PID == panePID`. The loop starts one level up
   and would otherwise never see the frame itself, and `PidAncestorIncludes`
   counts that case as inside the tree.
2. **At the top of every iteration, against the current `ppid`, before the
   candidate lookup and before any early return.** Checking only after reading
   `ancestorInfo` would miss the commonest case of all — the first parent *is*
   the pane shell (measured depth 1) — whenever that same iteration also hits an
   early return.

**Is a completed walk actually reachable?** Measured on this machine
(2026-09-08, `ps -axo pid,ppid,comm`), for every pane currently running an
agent:

```
claude 47858 → -zsh 46435 (pane, depth 1) → tmux 4465 (depth 2) → launchd 1 (depth 3)
```

The chain reaches PID 1 at depth 3 against a cap of 5, and `panePID` is hit at
depth 1 — two levels of headroom. Requiring a *completed* walk is therefore
strict without being unsatisfiable. An npm launcher adds one level (spec §3.5),
still inside the cap. If a future setup does exceed it, the walk refuses rather
than guesses, which is the failure mode this whole design has chosen everywhere
else.

`ctx.Err()` is checked between process reads (it cannot interrupt one —
`readProcessInfoPlatform` has no context). On expiry, return what is decided so
far as an error the handler turns into `found: false`.

`newMemoProcReader` caches by PID for the life of one request. It must be
created per request in Task 7, never package-level.

**Do not call `pidAncestorIncludesFn` / `PidAncestorIncludes`
(`probe/liveness.go:303`)**: it walks with no depth cap and calls
`agentpkg.ReadProcessInfo` directly, bypassing both the memo and the test seam.
**Do not reuse the projection's pane filter (`frame_ops.go:951-978`)**: it
*keeps* a frame when resolution fails, the opposite of the policy here.

- [ ] **Step 1: Write the failing tests** — table-driven over frame layouts:
  - one live root with an id → returned;
  - a root with an empty `session_id` → still returned by `resolvePaneOwners`
    (Task 7 filters it), asserted so the layering is explicit;
  - **pane id reused, old agent still alive, old row not swept → excluded**
    (the round-2 Blocker case);
  - `frame.PID == panePID` → treated as inside the tree;
  - nested same-type → only the parent is a root;
  - proxy-collapsed cross-type (child has no frame) → the parent is the root;
  - a stale frame (start-time mismatch) does not shadow a live root;
  - an unreadable process mid-walk → that frame excluded, others unaffected;
  - a chain longer than `proxyMaxDepth` → excluded;
  - **`panePID` seen but the cap exhausted → excluded.** `SawPanePID` never
    rescues an incomplete walk;
  - **the deepest walk that still completes → kept.** Mind where the root check
    lives: `ancestor.go:56` tests `ppid <= 1` at the **top of an iteration**, and
    a loop that runs out of iterations falls through to `Indeterminate`
    (`ancestor.go:112`). The last read that can still be *observed* is therefore
    the one taken at `depth = proxyMaxDepth-2`; a PPID 1 produced by the read at
    `proxyMaxDepth-1` has no further iteration to see it. With `proxyMaxDepth`
    5, `panePID` 200, and no framed ancestors:

    | Stage | Read → result |
    |---|---|
    | before the loop | sender `100 → PPID 200` |
    | depth 0 | `200 → 300` (and `SawPanePID` is set here, at the entry check) |
    | depth 1 | `300 → 400` |
    | depth 2 | `400 → 500` |
    | depth 3 | `500 → 1` |
    | depth 4 | entry check sees `ppid == 1` → `Root`; PID 1 is never read |

    Assert the read sequence is exactly `[100, 200, 300, 400, 500]`. **Do not
    move or add a root check outside the loop to make a deeper fixture pass** —
    that would change `classifyAncestor`'s behaviour, which Task 5 froze;
  - **`ppid == 0` appears on the chain while `CheckPane: false`** → no pane
    match is recorded and the verdict is unchanged from today, pinning that the
    options struct replaced a `0` sentinel that would have been unsound;
  - **an early ancestor-frame hit stops the walk**: assert the reader is *not*
    called for PIDs above that ancestor, so "we stopped" is observable rather
    than merely plausible;
  - a root whose chain reaches PID 1 without passing `panePID` → excluded;
  - `resolvePanePIDFn` failing → empty result, no error escalation;
  - **memoization, asserted positively.** "at most once per PID" also passes for
    a walk that never happened, so the test fixes one topology and asserts all
    four of: the returned owners are correct; the set of PIDs read equals the
    expected set exactly; each was read exactly once, including the shared
    ancestor; and a PID whose read failed is not retried within the request.
    Stub liveness, start time and pane PID so the branch under test is actually
    entered;
  - a cancelled context stops between reads.
- [ ] **Step 2: Run — fail**
- [ ] **Step 3: Implement**
- [ ] **Step 4: Run — pass**
- [ ] **Step 5:** `go test ./...`
- [ ] **Step 6: Commit** — `feat(agent): resolve a pane's root agent frames`

---

### Task 7: `GET /api/sessions/{code}/provenance`

**Files:**
- Create: `internal/module/agent/provenance_handler.go`, `..._test.go`
- Modify: `internal/module/agent/module.go` (route registration)
- Modify: `internal/tmux/executor.go` — add `PaneSessionID(target string)
  (string, error)` to the `Executor` interface and to `RealExecutor`. It mirrors
  `PaneSessionName` (`executor.go:291-297`) with `#{session_id}` instead of
  `#{session_name}` — a five-line method.
- **Modify: `internal/tmux/fake_executor.go`** — the matching method and a
  `SetPaneSessionID` seam **independent of** `SetPaneSessionName`
  (`fake_executor.go:391`), so a test can make the two disagree. Note
  `FakeExecutor.RenameSession` (`fake_executor.go:235`) preserves ids but does
  not touch `paneSessions`, which is what makes the rename-swap fixture
  expressible. The three test wrappers that embed `*tmux.FakeExecutor`
  (`probe/activity_test.go:21`, `codex/probe_intent_screen_change_test.go:602`,
  `module/agent/probe_orchestrator_test.go:53`) inherit the new method and need
  no edit.

**Response:**
```json
{ "found": true, "agent_type": "cc", "session_id": "…", "cwd": "…",
  "tmux_pane_id": "%12", "tmux_instance": "4465:…", "last_seen_at": 1788800000000 }
{ "found": false, "tmux_instance": "…" }
```

**Context.** Model the handler on `internal/module/session/cwd_handler.go`,
including its **two-sided generation sampling**: read `tmux_instance` before and
after the frame work and report `""` when the samples disagree or a read fails.
`""` authorises nothing on the SPA side, so this is the safety property, not a
nicety.

**How to enumerate the session's panes — through the frames, and through the
session *id*, never the name.** The `Executor` interface cannot list a session's
panes (`ActivePaneMetadata` is the active pane only), so go through the frames,
which are the only panes that can possibly answer:

1. `frames.ListAll()` → the distinct `pane_id`s that have any frame;
2. for each, `m.tmux.PaneSessionID(paneID)` → the tmux session id (`$N`), then
   `session.EncodeSessionID(id)` (`codec.go:22`, pure, returns
   `(string, error)`), and keep the panes whose code equals `{code}`. **Any
   error at either step excludes that pane** — no fallback to a name lookup;
3. run `resolvePaneOwners` on those.

A pane with no frame has no agent to report, so skipping it costs nothing.

**Do not use `m.resolvePaneSession` (`module.go:644`) here**, even though it
looks like exactly this function. It resolves pane → session *name* → code
through `LookupCodeByName`, whose cache is deliberately stale for up to 250 ms
after an external mutation (`session/lookup.go:12,23`). That is fine on the hook
hot path it was built for, and wrong here: rename session1 away and session2
into its name inside that window, and a query for session1's code can be
answered with session2's agent — same tmux server, so the generation stamp
matches and the pane-tree check passes too. A tmux session id is immutable for
the life of the session and `EncodeSessionID` is a pure function of it, so this
route has no such window. It is the same reason `handler.go` prefers
`TmuxSessionID` over the name whenever a hook carries one.

Build **one** memoized reader and **one** 5 s context for the whole request, and
pass them to `resolvePaneOwners` for every pane. Collect owners with a non-empty `session_id`; none →
`found: false`; several → largest `LastSeenAt`, ties broken by **ascending
`FrameID`** so the answer is deterministic and testable.

An unknown session code returns `found: false` with a 200, not a 404 — the SPA
treats "no answer" uniformly and a dead code is a normal race.

- [ ] **Step 1: Write the failing tests** — one root; no roots; a root whose
  `session_id` is empty → `found: false` (the filter lives here, not in Task 6);
  two roots across two panes of the same session (recency, then equal
  `LastSeenAt` → ascending `FrameID`); **two panes in two different windows of
  the same session** are both considered, so an implementation that only ever
  looks at the active pane fails; a framed pane belonging to a *different*
  session is not considered; **the rename-swap case** — the fake reports session
  ids that encode to the right codes while the *names* have been swapped between
  two live sessions, so an implementation routed through `PaneSessionName` fails
  here and only here. Build it so it cannot pass by accident, and **do not drive
  it through the real `LookupCodeByName`** — its 250 ms TTL would decide the
  outcome by timing rather than by implementation. Pin the name→code map with a
  fake provider (`fast_path_test.go:21`'s `fakeFastSessionProvider` already
  exposes `lookup`) holding the **pre-swap** mapping `A → code($0)`,
  `B → code($1)`; set the panes' *names* to the post-swap values while leaving
  `SetPaneSessionID` untouched; then query `code($0)` and assert the answer's
  `session_id` and `tmux_pane_id` are `$0`'s — not merely that `found` is true,
  which a crossed-over answer would also satisfy; unknown code → `found: false` with a 200; a
  disagreeing generation sample → `tmux_instance: ""`; the deadline expiring →
  `found: false`; and **memoization across two panes**, asserted the same
  positive way as Task 6 — the exact PID set, once each, including the ancestor
  the two panes share.
- [ ] **Step 2: Run — fail**
- [ ] **Step 3: Implement**
- [ ] **Step 4: Run — pass**
- [ ] **Step 5:** `go test ./...`
- [ ] **Step 6: Commit** — `feat(agent): serve the owning agent of a tmux session`

---

# Phase 3 — The SPA asks, and writes

### Task 8: A hand-typed cwd is marked as one

**Files:**
- Modify: `spa/src/types/tab.ts` (`cwdSource` union)
- Modify: `spa/src/stores/useTabStore.ts` (`field` arm, ~line 209)
- Modify: `spa/src/stores/useTabStore.rebuild.test.ts`

**Context.** `cwdSource` gains `'user'` and `'agent-backfill'`. The `field` patch
for `cwd` sets `cwdSource: 'user'`. Small and standalone, but Task 9 is wrong
without it: a hand-typed cwd currently keeps whatever source it inherited
(usually `'pane-probe'`) and the backfill's fill mode would overwrite it.

Check every reader of `cwdSource` before widening the union and update any
exhaustive switch.

**The `field` arm has a same-value early return in front of it**
(`useTabStore.ts:206`, `if (prev[patch.field] === patch.value) return c`). A user
who re-types the directory the probe already found is *confirming* it, and must
end up with `cwdSource: 'user'` — otherwise Task 9's fill mode may still
overwrite the value they just approved. So the early return must not fire for a
`cwd` patch whose value matches but whose `cwdSource` is not yet `'user'`.
Narrow that condition; do not delete it — it still protects the other two
fields.

- [ ] **Step 1: Write the failing test** — editing `cwd` sets `cwdSource: 'user'`;
  **submitting the *same* cwd that a probe supplied also sets it**, and returns
  a new object; submitting the same cwd when it is already `'user'` is still a
  no-op that returns the identical object; a subsequent `probe-cwd` patch still
  does not overwrite it (existing rule).
- [ ] **Step 2: Run — fail** · [ ] **Step 3: Implement** · [ ] **Step 4: Run — pass**
- [ ] **Step 5:** `pnpm --prefix spa exec vitest run` + `lint`
- [ ] **Step 6: Commit** — `feat(rebuild): mark a hand-typed cwd as user-sourced`

---

### Task 9: The `agent-backfill` patch — four ordered modes

**Files:**
- Modify: `spa/src/types/tab.ts` (`RebuildPatch`)
- Modify: `spa/src/stores/useTabStore.ts` (`setPaneRebuild`)
- Modify: `spa/src/stores/useTabStore.rebuild.test.ts`

**Context.** Spec §5.5. Implement as an **ordered** decision — first match wins —
because v3's unordered table let one state match two rows:

| # | Condition | Mode |
|---|---|---|
| 1 | `prev.agent` absent | **fill** |
| 2 | `prev.unverified` and the answer's `type` or `sessionId` differs | **replace** |
| 3 | `prev.unverified` and the identity matches | **confirm** |
| 4 | otherwise | **no-op** |

**Phase 3 predates the override field, so it touches only `resumeCommand`.**
`resumeCommandOverride` does not exist until Task 12 and the old field is not
removed until Task 13; writing Phase 3 against the final field names would not
type-check. Where the spec's mode descriptions mention the override, Phase 3
does the equivalent thing to `resumeCommand`, and Task 13 migrates it.

- **fill** — write `agent`; write `cwd` only if the answer has one *and* the
  existing `cwd` is absent or `cwdSource === 'pane-probe'`; set
  `cwdSource: 'agent-backfill'` when writing one; write `resumeCommand` (via the
  existing `composeResumeCommand`) **only when it is currently empty**, so a
  hand-typed command survives.
- **replace** — whole group as one unit, exactly like `agent-group`: new
  `agent`, the answer's `cwd` or none, `unverified` cleared, `resumeCommand`
  recomposed. A `cwdSource: 'user'` cwd is the one thing kept.
- **confirm** — clear `unverified`, change nothing else. This is what makes the
  probe terminate; without it an agreeing answer would leave the pane eligible
  forever.
- **no-op** — return the content unchanged (`return c`), like the other
  no-op arms.

The generation guard is unchanged: panes are matched on
`(hostId, sessionCode, tmuxInstance)`.

- [ ] **Step 1: Write the failing tests** — one per mode, including the case v3
  left ambiguous (agent present, same identity, **verified** → no-op, matched by
  row 4 and by no earlier row); a `'user'` cwd survives both fill and replace; a
  `'pane-probe'` cwd is replaced by fill; an `'agent-session-start'` cwd is not;
  a hand-typed `resumeCommand` survives fill but not replace; the generation
  guard rejects a mismatched instance; a later `agent-group` still overwrites
  everything.
- [ ] **Step 2: Run — fail** · [ ] **Step 3: Implement** · [ ] **Step 4: Run — pass**
- [ ] **Step 5:** vitest + lint
- [ ] **Step 6: Commit** — `feat(rebuild): fill, correct or confirm a record from the daemon's answer`

---

### Task 10: The provenance request and its generation rules

**Files:**
- Modify: `spa/src/lib/host-api.ts` (`fetchSessionProvenance`)
- Create: `spa/src/lib/rebuild/provenance-probe.ts` + `.test.ts`

**Context.** A sibling of `cwd-probe.ts`, reusing its **named helpers** rather
than paraphrasing its rules. Two comparisons that look alike and are not:

- **pane eligibility** uses `generationMatchesLegacy` (`binding.ts:46`), which is
  one-way: a pane whose recorded instance is `''` matches a known expected
  generation;
- **authorising the write** requires the answered generation to be non-empty
  **and** equal to the one asked with.

`disowned` is recorded **only when requested and answered are both non-empty and
different**. An answered `''` blocks the write and stays retryable; a requested
`''` likewise.

A pane wants a probe when it is live, terminal-mode, generation-eligible, and
either `rebuild.agent` is absent or `rebuild.unverified` is true.

This task ships the request path with a plain `inFlight` guard only. The
scheduler is Task 11, so keep the trigger surface to a single exported
`probeSessionProvenance(hostId, sessionCode, tmuxInstance)` that Task 11 wraps.

Export a `resetProvenanceProbes()` test seam like `resetCwdProbes`.

- [ ] **Step 1: Write the failing tests** — attach gate closed → no request;
  empty host/code → no request; in-flight dedup; both-non-empty-and-different
  disowns; an answered `''` does not disown but blocks the write; a requested
  `''` likewise; a pane re-pointed mid-flight takes nothing; a pane with
  `unverified` asks even though it has an agent; a fetch rejection is swallowed.
  **And the positive case, which the negatives cannot stand in for:** a
  `found: true` answer with a matching generation maps every field onto the
  `agent-backfill` patch and the record actually changes.
- [ ] **Step 2: Run — fail** · [ ] **Step 3: Implement** · [ ] **Step 4: Run — pass**
- [ ] **Step 5:** vitest + lint
- [ ] **Step 6: Commit** — `feat(rebuild): ask the daemon which agent owns a pane`

---

### Task 11a: The defer-never-drop scheduler

**Files:**
- Modify: `spa/src/lib/rebuild/provenance-probe.ts` + `.test.ts`

**Context — this task carries review round 4's finding 1. Read spec §5.4.1 in
full before starting.** Per binding:

```ts
{ nextAllowedAt: number; pending: boolean; timer: ReturnType<typeof setTimeout> | null }
```

**One scheduling entry point.** Every trigger calls it; it decides:

- in flight → set `pending = true` and return. **Arm no timer** — the
  completion handler owns what happens next.
- not in flight and `now >= nextAllowedAt` → run the request now.
- not in flight and still cooling → set `pending = true` and, if no timer is
  armed, arm one for `nextAllowedAt`.

**On completion** (success or failure): set `nextAllowedAt = Date.now() + 30_000`,
clear `inFlight`, and if `pending` is set, arm a timer for the **new**
`nextAllowedAt` — do **not** run immediately. Firing on completion would let a
slow request with continuous hooks run back to back and break the cost contract.

**Later triggers never move `nextAllowedAt`.** It is computed only at
completion. That is what stops a busy session from starving its own deferred
run, which a debounce would do.

**The timer callback re-checks everything** before starting a request: not in
flight, `now >= nextAllowedAt`, not `disowned`, attach gate open, and the pane
still eligible. It clears `pending` and the timer handle when it starts a
request, and also when it decides not to — a stale handle would make the
scheduler believe a timer is already armed.

Coalescing is the point: ten hooks in one cooldown buy one request.

A rejected request enters the cooldown exactly like a resolved one — otherwise a
host that is briefly down would be hammered. `resetProvenanceProbes()` must
`clearTimeout` every armed timer as well as clearing the maps, or one test's
timer fires inside the next.

- [ ] **Step 1: Write the failing tests** (fake timers)
  Every timing assertion names an exact instant, because "eventually" would pass
  against the implementations this task exists to rule out.

  - **the single-hook case**: request completes at t=0 with `found: false`, one
    hook at t=5 s, then silence → a request starts at t=30 s. This is the
    round-3 Blocker and it fails against drop-on-cooldown;
  - **no deadline extension**: hooks at t=5/10/15/20 s → exactly one deferred
    request, starting at t=30 s, **not** t=50 s;
  - **the boundary**: at t=29.999 s still exactly one request has been made;
  - **coalescing**: ten hooks in one cooldown → one request;
  - **in-flight**: a request that completes at t=40 s with a hook received at
    t=10 s schedules its follow-up for **t=70 s**, not t=40 s — the deadline is
    computed at completion and the follow-up waits for it;
  - **a rejected request** sets the cooldown just like a resolved one;
  - **guards on the deferred run**: a pane that gained an agent, was re-pointed,
    terminated, was disowned, or lost its attach gate during the cooldown issues
    no request — and the timer handle is cleared in each case, so a later
    trigger can arm a fresh one;
  - **regained eligibility**: a pane that was ineligible when the timer fired
    can be scheduled again by the next trigger;
  - **re-point away and back** during a cooldown leaves no stale timer handle;
  - **`resetProvenanceProbes()` cancels armed timers** — advancing the clock
    after a reset fires nothing;
  - **termination**: after a `confirm`, no further request on any number of
    triggers.
- [ ] **Step 2: Run — fail** · [ ] **Step 3: Implement** · [ ] **Step 4: Run — pass**
- [ ] **Step 5:** vitest + lint + build
- [ ] **Step 6: Commit** — `feat(rebuild): defer a suppressed provenance probe instead of dropping it`

---

### Task 11b: Wire the three triggers

**Files:**
- Modify: `spa/src/components/SessionPaneContent.tsx` + its test
- Modify: `spa/src/hooks/useMultiHostEventWs.ts` + its test

**Context.** A green Task 11a proves the scheduler, not that anything calls it.
These are separate failure modes — a handler that never fires, or one that
passes the wrong host or generation — and they need tests at the call sites, not
more scheduler tests.

1. **Session-list sweep** — in `useMultiHostEventWs`, beside the existing
   `probeMissingCwds(hostId)` call (~line 158), which already runs *after* the
   payload is reconciled and the attach gate opens. Same placement, same
   ordering: a sweep before reconciliation would ask with a generation the SPA
   has not adopted.
2. **Pane attach** — in `SessionPaneContent`, in the effect beside
   `probeSessionCwd` (~line 69), which already depends on the attach gate.
3. **Hook broadcast** — the new one, at the hook entry point
   (`useMultiHostEventWs.ts:167`). It must pass the **pane's recorded**
   generation, the same value the other two triggers pass — *not* anything read
   off the event — so all three requests share one binding key and one cooldown.
   Schedule **after** `handleNormalizedEvent` has run, so a broadcast that
   itself writes the record leaves the pane ineligible and costs no request.

- [ ] **Step 1: Write the failing tests** — a reconciled `sessions` payload
  schedules a sweep for each eligible binding and none for ineligible ones;
  mounting a pane schedules one; a hook broadcast for an eligible pane schedules
  one **with the pane's recorded generation**; a hook broadcast for a pane that
  already has a verified agent schedules none; nothing is scheduled before the
  attach gate opens.

  **The ordering test, which the "already verified" case cannot stand in for.**
  Start from a pane with no agent, deliver a `SessionStart` carrying a valid
  provenance envelope through the real socket path with `handleNormalizedEvent`
  **not** stubbed, and assert that the record ends up verified **and that no
  provenance request was scheduled**. Scheduling before normalization also
  passes the "already verified" case, because that fixture was already verified
  when the hook arrived; only an event that *causes* the transition
  distinguishes the two orderings.
- [ ] **Step 2: Run — fail** · [ ] **Step 3: Implement** · [ ] **Step 4: Run — pass**
- [ ] **Step 5:** vitest + lint + build
- [ ] **Step 6: Commit** — `feat(rebuild): ask for provenance on attach, sweep and hook`

**Phase 3 gate:** after this task, Phase 1 + 2 + 3 is independently shippable — a
pre-deploy session acquires its agent and a working resume command built from
the existing hardcoded shapes. Verify by hand against a real running session
before starting Phase 4.

---

# Phase 4 — Templates and the override

### Task 12: The template store and the resolver

**Files:**
- Create: `spa/src/stores/useResumeTemplateStore.ts` + `.test.ts`
- Modify: `spa/src/lib/rebuild/composer.ts` + `composer.test.ts`
- Modify: `spa/src/lib/storage/keys.ts` (a `STORAGE_KEYS` entry)
- Modify: `spa/src/types/tab.ts` — **add** `resumeCommandOverride?: string`
  beside the existing `resumeCommand`

**Context.** Model the store on `useNotificationSettingsStore.ts`: sparse
per-agent record, `purdexStorage`, `syncManager.register`. Defaults reproduce
today's shapes exactly, so a user who configures nothing sees no change.

`resolveResumeCommand(record, templates)`: override → template → `''`.
`SAFE_SESSION_ID` is unchanged and remains the only thing interpolated; an id
outside it degrades to `fallback`. `{id}` is replaced at **every** occurrence in
`exact`, and left **literal** in `fallback`.

**Add the override field here, remove the old one in Task 13.** The spec's
signature is `Pick<PaneRebuildRecord, 'agent' | 'resumeCommandOverride'>`, which
does not compile against a type that has no such key — so this task adds the
optional field (nothing writes it yet) and Task 13 deletes `resumeCommand`. Both
commits type-check on their own; a single swap would not.

Add `resolveResumeCommand` alongside `composeResumeCommand` here; Task 13 removes
the old one and every caller of it.

- [ ] **Step 1: Write the failing tests** — store defaults, per-agent set, reset,
  persistence shape; resolver across three layers × (usable id / unusable id / no
  id) × (override / no override); `{id}` at every occurrence; `fallback` keeps a
  literal `{id}`; unknown agent → `''`.
- [ ] **Step 2: Run — fail** · [ ] **Step 3: Implement** · [ ] **Step 4: Run — pass**
- [ ] **Step 5:** vitest + lint
- [ ] **Step 6: Commit** — `feat(rebuild): per-agent resume command templates`

---

### Task 13: `resumeCommand` → `resumeCommandOverride`, everywhere

**Files:** `types/tab.ts` (remove `resumeCommand`, keep the override),
`useAgentStore.ts:94`, `useTabStore.ts:200,209`,
`RebuildActionSet.tsx:18,229,294,298`, `RenamePopover.tsx:149,153`,
`TerminatedPane.tsx`, `batch.ts:68,82`, `engine.ts:164,496-502`,
`SnapshotSettingsSection.tsx:664`, and the Task 9 `agent-backfill` writer.

Test fixtures that reference the old field — the list is longer than spec §4.2's
and **must not be applied by blind search-and-replace**, because two of these
files also hold an *operation* `resumeCommand` that keeps its name:

| File | Note |
|---|---|
| `useTabStore.rebuild.test.ts` | record fixtures |
| `useAgentStore.provenance.test.ts:46,65,107,137` | still asserts the automatic `resumeCommand` write that this task removes — these assertions change meaning, not just names |
| `RebuildActionSet.test.tsx` | record fixtures **and** operation fixtures — only the former are renamed |
| `TerminatedPane.test.tsx:224` | record fixture — renamed |
| `TerminatedPane.test.tsx:253` | **operation** field — must NOT be renamed |
| `RenamePopover.rebuild.test.tsx`, `composer.test.ts`, `batch.test.ts`, `engine.test.ts` | record fixtures |

`eligibility.ts` has no reader of this field today; it is in scope only to
confirm that, not to change.

**Context.** Three rules that make this more than a rename:

1. **`useRebuildStore.ts:40`'s `resumeCommand` keeps its name.** It is the string
   an operation pinned, not the record field. Renaming it would blur exactly the
   distinction Task 14 depends on.
2. **The engine resolves once, at operation start**, and pins the result into the
   operation as it already does.
3. **Clearing the override is identity-scoped** (spec §4.3): a qualifying
   `SessionStart` clears it only when `agent.type` or `agent.sessionId` differs
   from what the record held. An idle re-emit with the same id keeps the user's
   edit. Task 9's `replace` mode already clears it; make sure the `agent-group`
   arm now matches that rule instead of clearing unconditionally.

`composeResumeCommand` and the `resumeCommand` write in Task 9's fill/replace
modes both disappear here. Task 9's fill mode preserved a hand-typed command;
after this task the equivalent value lives in `resumeCommandOverride`, and the
fill mode leaves it alone.

**An empty resolved command turns off the resume step; it does not exclude the
pane.** `planForRecord` (`batch.ts:64`) already sets
`runResume: !!record.resumeCommand && !record.unverified` while leaving
`createSession` and `applyCwd` on. That is spec §4.2's "an unknown agent
rebuilds as a shell", and it must survive the rename — turning an empty command
into an exclusion would be a real behaviour regression.

- [ ] **Step 1: Write the failing tests** — same identity keeps the override; a
  different sessionId clears it; a different type clears it; the engine sends
  the resolved string; **a pane resolving to `''` is still rebuilt with
  `createSession` and `applyCwd`, only `runResume` off, and no send-keys is
  issued**; editing a row writes an override and clearing it restores the
  template (spec §7).

  Plus the one thing "every consumer calls the resolver" does not prove:
  **changing a template in the store re-renders the panel, the popover and the
  Settings table without a remount.** Calling the resolver from an unsubscribed
  render path would pass every other test here and still show a stale command.
- [ ] **Step 2: Run — fail** · [ ] **Step 3: Implement** · [ ] **Step 4: Run — pass**
- [ ] **Step 5:** vitest + lint + build
- [ ] **Step 6: Commit** — `refactor(rebuild): the pane stores an override, not a command`

---

### Task 14: The panel shows what the next action would send

**Files:**
- Modify: `spa/src/components/RebuildActionSet.tsx` + `.test.tsx`
  (the pinning decision sits beside the existing `frozen` computation at
  line 226 — `busy || !!created || hostRemoved` — which already knows both
  states this rule needs; the Rebuild button it guards is at line 379)

**Context.** Spec §4.3. Use the **real** symbols — the component does not have
an `op.created`:

```ts
const busy    = op?.status === 'running'      // RebuildActionSet.tsx:214
const created = op?.report?.created           // RebuildActionSet.tsx:215
const pinned  = busy || !!created             // the new rule
```

`useRebuildStore`'s operation field is `createdSession`
(`useRebuildStore.ts:42`), but the component reads the operation through
`RebuildOperationView` (`RebuildActionSet.tsx:25`), which exposes
`report.created`. Stay on the view type — and **add `resumeCommand?: string` to
it**, since it does not declare one today and the pinned string has to arrive
through it.

Render `resolveResumeCommand(...)` except when `pinned`, in which case every row
renders `op.resumeCommand`. An operation that failed *before* creating anything
is not actionable, so the panel returns to the live resolution and a template
edited meanwhile is visible before the user presses Rebuild.

Fixtures must build operations in the real shape. A test that invents
`{ created: … }` at the top level would make a wrong implementation pass.

- [ ] **Step 1: Write the failing tests** — no operation → composed;
  `status: 'running'` → pinned, even after a template change from another
  window; `report.created` present on a finished operation → pinned;
  **`status: 'done'` with no `report.created` → composed, and the next Rebuild
  sends what is shown**.
- [ ] **Step 2: Run — fail** · [ ] **Step 3: Implement** · [ ] **Step 4: Run — pass**
- [ ] **Step 5:** vitest + lint
- [ ] **Step 6: Commit** — `fix(rebuild): pin the displayed command only while it is actionable`

---

# Phase 5 — The shell probe (daemon)

### Task 15: `POST /api/shell/resolve-command`

**Files:**
- Create: `internal/module/session/shell_resolve.go` + `_test.go`
- Modify: `internal/module/session/module.go` (route)
- Modify: `internal/tmux/executor.go` — add `ShowGlobalOption(option string)
  (string, error)` to the `Executor` **interface** (~line 80, beside
  `ShowWindowOption`) and to `RealExecutor` (~line 432)
- **Modify: `internal/tmux/fake_executor.go`** — the matching method plus a
  settable value/error seam, next to the existing `ShowWindowOption`
  (~line 613). `SessionModule.tmux` is the interface, so omitting this breaks
  compilation for every existing test in the package

**Context.** Spec §4.4. The contract is deliberately narrow:

```
400  malformed body (`command` missing or not a string)
200  { "resolved": true,  "detail": "<last non-empty stdout line>" }
200  { "resolved": false, "reason": "not_found" | "shell_metacharacters" | "too_long" | "timeout" | "shell_failed" }
```

**No `kind` field.** Two earlier designs tried to classify and both were wrong:
`type` output differs between zsh and bash (bash prints the whole function
body), and `command -v` output is not "path or word" (an alias prints its
definition; a relative PATH entry prints a relative path). Report that it
resolved and what the shell printed.

Shell selection: `tmux show-options -gv default-shell` via the new executor
method — `ShowWindowOption` passes `-w` and cannot read a global option. Fall
back to `$SHELL`, then the passwd shell, then `/bin/sh`. Invoke as an
interactive login shell:

```go
script := `builtin command -v "$1"`     // zsh, bash (by basename)
script  = `command -v "$1"`             // anything else
exec.CommandContext(ctx, shell, "-l", "-i", "-c", script, "_", token)
```

Process hygiene, all four required: `SysProcAttr{Setpgid: true}` with
`Cmd.Cancel` killing `-pgid`; `Cmd.WaitDelay = time.Second`; stdin `/dev/null`;
stdout/stderr through an `io.LimitReader` capped at 8 KiB *before* the 512-byte
display truncation. 5 s deadline.

Rejected before exec: a token over 256 bytes, or containing any of
`` | & ; < > ( ) $ ` \ " ' `` or a newline, or starting with `-`.

- [ ] **Step 1: Write the failing tests** — metacharacter and oversize rejection
  with **no exec** (assert through the shell-invocation function variable);
  malformed body → 400; a stub timing out → `reason: "timeout"`; exit 1 →
  `not_found`; exit 0 → the last non-empty line as `detail`, with rc chatter
  before it ignored. Two integration tests against the real shell: a builtin
  resolves; and an rc that spawns a long-lived descendant holding the output
  pipe still returns within the deadline with the process group gone. Cover the
  §4.4 shapes — absolute path, relative PATH entry, function, alias, builtin,
  keyword — asserting `resolved` only.

  Also the shell-selection ladder, which the verdict tests do not touch: the
  executor's answer is used when present; a tmux error falls back to `$SHELL`;
  an unset `$SHELL` falls back to the passwd shell and then `/bin/sh`. And one
  argv assertion that the tmux invocation uses the **global** option form, not
  `ShowWindowOption`'s `-w`.
- [ ] **Step 2: Run — fail** · [ ] **Step 3: Implement** · [ ] **Step 4: Run — pass**
- [ ] **Step 5:** `go test ./...`
- [ ] **Step 6: Commit** — `feat(session): resolve a command word in the pane's login shell`

---

# Phase 6 — Settings UI

### Task 16: The template editor

**Files:**
- Create: `spa/src/components/settings/ResumeTemplateSettings.tsx` + `.test.tsx`
- Modify: `spa/src/components/settings/SnapshotSettingsSection.tsx` (render it)
- Modify: `spa/src/lib/host-api.ts` (`resolveShellCommand`)
- Modify: `spa/src/locales/{en,zh-TW}.json`

**Context.** Spec §4.5. A separate file rendered inside the existing Snapshot
section — separate so it does not deepen issue #975, shared so the templates sit
next to the records they govern.

Agent rows come from `AGENT_NAMES`. Editing reuses the `EditableCwdCell` pattern
(alpha.324): `committedRef` against double submit, `disabled` while busy, and
`compositionRef` + `isComposing` so an IME Enter does not commit. Those were
review findings on that component; rediscovering them here is not acceptable.

Validation **warns and still saves**: `exact` without `{id}`, `fallback` with
`{id}`.

**A test result is keyed by `(hostId, commandWord)`** and shown only while both
still match. Switching the host picker or editing the row clears it, and a
response arriving after either changed is discarded — a verdict from another
machine must never sit beside a command being judged for this one.

- [ ] **Step 1: Write the failing tests** — rows render per agent with defaults;
  editing persists; reset restores defaults; the two warnings appear and do not
  block; **testing `cld-yolo --resume {id}` POSTs exactly `cld-yolo`**; Test
  renders each verdict; **a 404 renders as `unverifiable` and the template stays
  saved**; a network rejection likewise; switching hosts clears the result; a
  late response from the previous host is discarded; **a late response for a
  command word the user has since edited is discarded**; the picker defaults to
  the active host; IME composition does not commit; no literal English in the
  component (assert through i18n keys).
- [ ] **Step 2: Run — fail** · [ ] **Step 3: Implement** · [ ] **Step 4: Run — pass**
- [ ] **Step 5:** vitest + lint + build
- [ ] **Step 6: Commit** — `feat(settings): edit and test the resume command templates`

---

## Manual verification (not automatable)

The reboot path cannot be unit-tested and **the user has said they will run it
themselves** — do not schedule it, and do not kill any tmux server.

1. With a pre-deploy session open, confirm the resume row fills in without any
   user action (Phase 3 gate).
2. Set `cld-yolo --resume {id}` as the cc template; confirm the panel shows the
   composed command and Test reports it resolves.
3. `tmux kill-server`, then Rebuild each of cc / codex / opencode.
4. Uncheck the resume row → expect a bare shell in the right directory.

## Review items carried in

**From the spec's round 4** — spec §10.1:

- finding 1 → Task 11a's scheduling contract and its exact-instant test list;
- finding 2 → Task 6's boundary matrix and its tests.

**From the plan review** (codex `task-mtrhhdht-bl382f`, 1 Blocker / 11 Important
/ 1 Minor), all folded into the tasks above:

| # | Finding | Where it landed |
|---|---|---|
| B1 | `PaneOwner` had no `FrameID` for Task 7's tie-break; the walker's pane-membership handoff and its boundaries were undefined | Task 6: `FrameID`, the `ancestor.go` modification, the boundary matrix, the last-allowed-depth and early-stop tests |
| I1 | Task 3 named only the general return; two own-frame early returns were missing | Task 3's return-point table + per-path tests |
| I2 | Tasks 9/12/13 referenced fields that did not exist yet | Task 12 *adds* the override, Task 13 *removes* the old field; Phase 3 uses only the old one; a build step per task |
| I3 | No way to enumerate a session's panes — `Executor` has none | Task 7 goes through `frames.ListAll()` + a per-pane session-id lookup (superseded by round 2 finding 1 below, which replaced the name-based lookup) |
| I4 | "batch skips a pane that resolves to `''`" would have been a regression | Task 13: an empty command turns off `runResume` only |
| I5 | `op.created` does not exist | Task 14 uses `status` / `report.created`, and extends `RebuildOperationView` |
| I6 | Fixture list incomplete; two files mix record and operation fields; no reactivity test | Task 13's fixture table and the template-change re-render test |
| I7 | A green scheduler proves nothing about the triggers | Task 11 split into 11a (scheduler) and 11b (call sites) |
| I8 | "at most once per PID" also passes for a walk that never ran | Tasks 6 and 7 assert the exact PID set, positively |
| I9 | A new `Executor` method without `FakeExecutor` breaks compilation | Task 15's Files |
| I10 | Task 16 was missing five spec contracts | Task 16's Context and tests |
| I11 | The `field` arm's same-value early return would skip `cwdSource: 'user'` | Task 8's narrowed condition + its test |
| M1 | Reference drift: opencode guard lines, `storage/keys.ts`, `eligibility.ts` | corrected in place |

**From the second plan review** (codex `task-mtrhv4kc-507pse`, 2 Blocker /
2 Important):

| # | Finding | Where it landed |
|---|---|---|
| 1 | **Blocker, new** — routing pane→session through `resolvePaneSession` inherits `LookupCodeByName`'s 250 ms stale name cache, so a rename swap between two live sessions can answer one session's code with the other's agent | Task 7 goes through `PaneSessionID` + the pure `EncodeSessionID`; new `Executor`/`FakeExecutor` method; the rename-swap test |
| 2 | **Blocker** — the boundary matrix's rows were not mutually exclusive, and "keep a hit at the last allowed depth" contradicted "an incomplete walk is excluded" | Task 6's matrix is rewritten by termination reason; the rule is `Root && SawPanePID`; `SawPanePID` is explicitly observational; the measured 3-deep chain shows the strict rule is satisfiable |
| 2b | `panePID == 0` is not a safe "disabled" sentinel — a PPID of 0 is representable | an `ancestryOpts{PanePID, CheckPane}` struct, plus a `ppid == 0` test |
| 3 | Task 3 missed a fourth own-frame return (`subagent_id_missing`), mislabelled the `StopFailure` detach as proxy, and implied the parent list was exhaustive | Task 3's table is rewritten around the governing rule, with the fourth return and its test |
| 4 | Task 11b's tests could not catch scheduling *before* normalization | the ordering test, using an event that *causes* the transition |

**From the third plan review** (codex `task-mtri4jku-kvcoe5`, 1 Blocker,
1 Important, 1 Minor):

| # | Finding | Where it landed |
|---|---|---|
| 1 | **Blocker** — round 2's Task 7 fix existed only in the disposition table; the task body still specified `resolvePaneSession`. (A revision script aborted on an assertion before writing, and the commit message claimed the fix anyway.) | Task 7's Files and Context now specify `PaneSessionID` + `EncodeSessionID` with its error handling, and the rename-swap fixture is spelled out |
| 2 | Important — "a hit at the last allowed depth, with `ppid <= 1` on the next read" cannot happen: the root check is at the top of an iteration and an exhausted loop falls through to `Indeterminate` | the test is restated as an exact PID sequence whose second-to-last read yields PPID 1, with an explicit ban on moving the root check |
| 2b | `SawPanePID` must also be accumulated at the top of each iteration, before the candidate lookup — otherwise the commonest case (the first parent *is* the pane) is missed whenever that iteration returns early | the two-places rule in Task 6 |
| 3 | Minor — the six rows are exhaustive only over `err == nil` terminations | the table's scope is stated, and the two error paths are named |

Confirmed by that round and needing no change: no fifth own-frame return exists
(`frame_ops.go:121` carries the sender's frame id but its row was already
deleted at `:117`, so it must not write); `ancestryOpts` with `CheckPane:false`
adds no process read and changes no ordering; every `tmux.Executor`
implementation outside the two real ones embeds `*tmux.FakeExecutor` and
inherits new methods; and Task 11b's ordering test is expressible against the
existing `FakeSocket` without stubbing normalization.

Also adopted from the first review: **do not memoize `classifyAncestor`**
(`provenance_test.go:170` depends on successive reads differing), and **a green
`provenance_test.go` does not prove Task 3's wiring** — its fixtures' providers
do not implement `SessionIdentifier`.
