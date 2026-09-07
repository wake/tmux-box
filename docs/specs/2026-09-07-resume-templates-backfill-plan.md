# Resume Templates & Provenance Backfill — Implementation Plan

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
  report** instead of editing it. The same rule applies to
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
  internal (`package opencode`).
- **The two identity columns never travel inside a `Frame` round-trip write.**
  Spec §5.2 has the per-method table; Task 1 implements it and Task 3 depends on
  it. Any task that adds a column to `UpsertIfUnchanged`, `UpdateHookPath`,
  `UpdateHookPathAndResetSubagents` or `UpdateStatusAndLastSeen` is wrong.

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
| `internal/agent/opencode/plugin_template.go` *(modify)* | `cwd` on `PdxStop` / `PdxUserPromptSubmit` |
| `internal/module/session/shell_resolve.go` *(new)* | `POST /api/shell/resolve-command` |
| `internal/tmux/executor.go` *(modify)* | Global (`-g`) option read for `default-shell` |

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

Migration: additive `ALTER TABLE … ADD COLUMN`, guarded by a column-existence
check, next to the existing `clearStaleSubagentsJSON` step (`frames.go:62`).
Existing rows get `''`, which is correct — the frame has not told us yet.

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
Malformed JSON returns `("", "")` and never an error — a hook payload we cannot
parse is not an error condition for this feature.

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
  parent's row would be a real mis-attribution. Guard by reaching the call only
  from return points where the frame is the sender's own.
- `sql.ErrNoRows` is logged and swallowed; a frame deleted concurrently is not
  an error for the caller.
- A provider that does not implement `SessionIdentifier` skips the call.

- [ ] **Step 1: Write the failing tests**
  - an ordinary event (`PdxStop`) on an existing frame writes the id;
  - a second ordinary event carrying no id leaves it;
  - an ordinary event carrying a **different** id replaces it (opencode's
    in-process session switch, spec §3.3);
  - a `cwd` arriving on a later event than the id fills in;
  - a `SessionStart` with `source == "compact"` (rejected upstream by
    `deriveCCStatus`) writes nothing;
  - **a proxy-collapsed sender writes nothing to the parent's frame** — reuse
    the proxy fixtures in `frame_ops_test.go`;
  - interleaving: an identity write between a proxy attach's read and its
    `UpsertIfUnchanged` retry leaves both the merged subagents list and the new
    id intact.
- [ ] **Step 2: Run — fail**
- [ ] **Step 3: Implement**
- [ ] **Step 4: Run — pass**
- [ ] **Step 5:** `go test ./...`. `provenance_test.go` must be green **unedited**.
- [ ] **Step 6: Commit** — `feat(agent): record the session id from ordinary hook events`

---

### Task 4: OpenCode emits `cwd` on more than `SessionStart`

**Files:**
- Modify: `internal/agent/opencode/plugin_template.go` (lines ~145, ~177)
- Modify: `internal/agent/opencode/plugin_template_test.go` and
  `plugin_template_bun_integration_test.go` (internal package)

**Context.** Add `cwd: pdxCwd()` to the `PdxStop` and `PdxUserPromptSubmit`
emits. `pdxCwd()` already exists (line ~75) and already returns `''` rather than
throwing. **Do not touch the `parentID` child-session filter** (lines 47, 97) —
spec v1 §9.3 states it is a precondition of the ownership invariant for opencode.

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

# Phase 2 — The ownership query (daemon)

### Task 5: Extract the traversal from `classifyAncestor` (pure refactor)

**Files:**
- Modify: `internal/module/agent/ancestor.go`
- Modify: `internal/module/agent/ancestor_test.go`

**Interfaces produced:**
```go
// procReader is the seam: classifyAncestor passes readProcessInfoFn directly,
// Task 6 passes a memoizing wrapper around it.
type procReader func(pid int) (agentpkg.ProcessInfo, error)

// walkPaneAncestry walks startPID's PPID chain, capped at proxyMaxDepth,
// applying the existing liveness + identity gating to each candidate frame.
func (m *Module) walkPaneAncestry(
    paneID string, startPID int, agentType string, read procReader,
) (AncestorVerdict, *store.Frame, error)
```

**Context.** This is a **pure refactor**. `classifyAncestor` becomes:

```go
func (m *Module) classifyAncestor(req EventRequest) (AncestorVerdict, *store.Frame, error) {
    if m.frames == nil { return VerdictIndeterminate, nil, nil }
    info, err := readProcessInfoFn(req.SenderPID)
    if err != nil { return VerdictIndeterminate, nil, nil }
    return m.walkPaneAncestry(req.TmuxPaneID, info.PPID, req.AgentType, readProcessInfoFn)
}
```

Note the loop **starts from the PPID**, not from the PID — the existing code
reads the sender's own info first and then walks from `info.PPID`
(`ancestor.go:51-56`). Preserve that exactly, along with the depth cap, the
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

**Interfaces produced:**
```go
type PaneOwner struct {
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

Three boundary rules the existing loop does not give you for free:

1. **`frame.PID == panePID` must be handled before the loop.** The walk starts
   from the PPID, so a frame whose PID *is* the pane process would never see
   itself. `PidAncestorIncludes` treats that as inside the tree; match it.
2. **An early ancestor hit ends the walk without deciding pane membership, and
   that is fine.** The frame is already not a root, so it is excluded either
   way. Do not extend the walk to establish membership you no longer need.
3. **The depth cap applies to both questions.** Measured depth is 1 for an
   agent under its pane shell and 2 for an npm launcher (spec §3.5); the cap is
   5. A chain that exceeds it yields "cannot determine" → the frame is
   **excluded**, never promoted.

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
  - `resolvePanePIDFn` failing → empty result, no error escalation;
  - **memoization**: a layout where three frames share an ancestor chain asserts
    the underlying reader is called once per distinct PID for the whole call;
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

Resolve `{code}` → tmux session → its panes. Build **one** memoized reader and
**one** 5 s context for the whole request, and pass them to `resolvePaneOwners`
for every pane. Collect owners with a non-empty `session_id`; none →
`found: false`; several → largest `last_seen_at`, ties broken by frame id so the
answer is deterministic.

An unknown session code returns `found: false` with a 200, not a 404 — the SPA
treats "no answer" uniformly and a dead code is a normal race.

- [ ] **Step 1: Write the failing tests** — one root; no roots; two roots across
  two panes (recency, then the tie-break); unknown code; a disagreeing
  generation sample → `tmux_instance: ""`; the deadline expiring →
  `found: false`; and **one memoized reader across two panes** (the reader is
  called once per distinct PID for the request, not once per pane).
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

- [ ] **Step 1: Write the failing test** — editing `cwd` sets `cwdSource: 'user'`;
  a subsequent `probe-cwd` patch still does not overwrite it (existing rule).
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

- **fill** — write `agent`; write `cwd` only if the answer has one *and* the
  existing `cwd` is absent or `cwdSource === 'pane-probe'`; set
  `cwdSource: 'agent-backfill'` when writing one; leave `resumeCommandOverride`
  alone; write `resumeCommand` (via the existing `composeResumeCommand`) **only
  when it is currently empty**, so a hand-typed command survives.
- **replace** — whole group as one unit, exactly like `agent-group`: new
  `agent`, the answer's `cwd` or none, `unverified` cleared,
  `resumeCommandOverride` cleared, `resumeCommand` recomposed. A
  `cwdSource: 'user'` cwd is the one thing kept.
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
- [ ] **Step 2: Run — fail** · [ ] **Step 3: Implement** · [ ] **Step 4: Run — pass**
- [ ] **Step 5:** vitest + lint
- [ ] **Step 6: Commit** — `feat(rebuild): ask the daemon which agent owns a pane`

---

### Task 11: The defer-never-drop scheduler and its three triggers

**Files:**
- Modify: `spa/src/lib/rebuild/provenance-probe.ts` + `.test.ts`
- Modify: `spa/src/components/SessionPaneContent.tsx`
- Modify: `spa/src/hooks/useMultiHostEventWs.ts`

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

**Three triggers:**
1. the reconciled `sessions` payload in `useMultiHostEventWs` (a sweep, next to
   the existing `probeMissingCwds` call, ~line 160);
2. pane attach in `SessionPaneContent` (~line 59, next to `probeSessionCwd`);
3. **a hook broadcast for a session whose pane still wants provenance** — the
   new one. Without it the everyday case fails: the first probe runs before any
   event has filled the frame's `session_id`, gets `found: false`, and nothing
   asks again.

- [ ] **Step 1: Write the failing tests** (fake timers)
  - **the single-hook case**: `found: false` at t=0, one hook at t=5 s, then
    silence → a request runs at t≈30 s. This is the round-3 Blocker and it fails
    against a drop-on-cooldown implementation;
  - **no deadline extension**: hooks at t=5/10/15/20 s → exactly one deferred
    request, at t≈30 s, not t≈50;
  - **coalescing**: ten hooks in one cooldown → one request;
  - **in-flight**: a hook during an open request → exactly one follow-up, and it
    runs at the *new* deadline, not at completion;
  - **guards on the deferred run**: a pane that gained an agent, was re-pointed,
    terminated, was disowned, or lost its attach gate during the cooldown issues
    no request — and the timer handle is cleared in each case;
  - **re-point away and back** during a cooldown leaves no stale timer handle;
  - **termination**: after a `confirm`, no further request on any number of
    broadcasts.
- [ ] **Step 2: Run — fail** · [ ] **Step 3: Implement** · [ ] **Step 4: Run — pass**
- [ ] **Step 5:** vitest + lint + build
- [ ] **Step 6: Commit** — `feat(rebuild): defer a suppressed provenance probe instead of dropping it`

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
- Modify: `spa/src/lib/storage` (a `STORAGE_KEYS` entry)

**Context.** Model the store on `useNotificationSettingsStore.ts`: sparse
per-agent record, `purdexStorage`, `syncManager.register`. Defaults reproduce
today's shapes exactly, so a user who configures nothing sees no change.

`resolveResumeCommand(record, templates)`: override → template → `''`.
`SAFE_SESSION_ID` is unchanged and remains the only thing interpolated; an id
outside it degrades to `fallback`. `{id}` is replaced at **every** occurrence in
`exact`, and left **literal** in `fallback`.

Add `resolveResumeCommand` alongside `composeResumeCommand` in this task; Task 13
removes the old one.

- [ ] **Step 1: Write the failing tests** — store defaults, per-agent set, reset,
  persistence shape; resolver across three layers × (usable id / unusable id / no
  id) × (override / no override); `{id}` at every occurrence; `fallback` keeps a
  literal `{id}`; unknown agent → `''`.
- [ ] **Step 2: Run — fail** · [ ] **Step 3: Implement** · [ ] **Step 4: Run — pass**
- [ ] **Step 5:** vitest + lint
- [ ] **Step 6: Commit** — `feat(rebuild): per-agent resume command templates`

---

### Task 13: `resumeCommand` → `resumeCommandOverride`, everywhere

**Files:** the full list in spec §4.2 — `types/tab.ts`, `useAgentStore.ts:94`,
`useTabStore.ts:200,209`, `RebuildActionSet.tsx:18,226,229,294,298,379`,
`RenamePopover.tsx:149,153`, `TerminatedPane.tsx`, `batch.ts:68,82`,
`eligibility.ts`, `engine.ts:164,496-502`, `SnapshotSettingsSection.tsx:664`,
plus the seven fixture files.

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
modes both disappear here.

- [ ] **Step 1: Write the failing tests** — same identity keeps the override; a
  different sessionId clears it; a different type clears it; every renamed
  consumer reads through `resolveResumeCommand`; the engine sends the resolved
  string; `batch` skips a pane that resolves to `''`.
- [ ] **Step 2: Run — fail** · [ ] **Step 3: Implement** · [ ] **Step 4: Run — pass**
- [ ] **Step 5:** vitest + lint + build
- [ ] **Step 6: Commit** — `refactor(rebuild): the pane stores an override, not a command`

---

### Task 14: The panel shows what the next action would send

**Files:**
- Modify: `spa/src/components/RebuildActionSet.tsx` + `.test.tsx`

**Context.** Spec §4.3. Render `resolveResumeCommand(...)` **except** while the
pane's operation is in flight or has `op.created` — in those two states every
row renders `op.resumeCommand`. An operation that failed *before* creating
anything is not actionable, so the panel returns to the live resolution and a
template edited meanwhile is visible before the user presses Rebuild.

- [ ] **Step 1: Write the failing tests** — no operation → composed; in flight →
  pinned, even after a template change from another window; `op.created` present
  → pinned; **create failed with no `op.created` → composed, and the next
  Rebuild sends what is shown**.
- [ ] **Step 2: Run — fail** · [ ] **Step 3: Implement** · [ ] **Step 4: Run — pass**
- [ ] **Step 5:** vitest + lint
- [ ] **Step 6: Commit** — `fix(rebuild): pin the displayed command only while it is actionable`

---

# Phase 5 — The shell probe (daemon)

### Task 15: `POST /api/shell/resolve-command`

**Files:**
- Create: `internal/module/session/shell_resolve.go` + `_test.go`
- Modify: `internal/module/session/module.go` (route)
- Modify: `internal/tmux/executor.go` (a global-option read)

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

Shell selection: `tmux show-options -gv default-shell` via a **new** executor
method — `ShowWindowOption` passes `-w` and cannot read a server option. Fall
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
  block; Test renders each verdict; switching hosts clears the result; a late
  response from the previous host is discarded; IME composition does not commit;
  no literal English in the component (assert through i18n keys).
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

## Review items carried from the spec

- **Round 4 finding 1** → Task 11's scheduling contract and its test list.
- **Round 4 finding 2** → Task 6's three boundary rules and their tests.
- Everything else is dispositioned in spec §10 and needs no plan action.
