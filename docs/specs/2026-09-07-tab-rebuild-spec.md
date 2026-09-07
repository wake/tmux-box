# Spec — Per-tab session provenance & one-click rebuild ("Tab Rebuild")

Status: draft v1
Date: 2026-09-07
Branch: `worktree-tab-rebuild`

## 1. Problem

When the host reboots, every tmux session dies. The tabs in Purdex survive
(the tab store is persisted), but each one is now pointing at a session code
that no longer exists. Restoring a working environment today means, per tab:
remembering which project it was, recreating the session by hand, `cd`-ing to
the right place, and re-launching the agent with the right resume flag.

Three mechanisms exist and none of them close this:

| Mechanism | What it does | Why it is not enough |
|---|---|---|
| `TerminatedPane` (`spa/src/components/TerminatedPane.tsx`) | Lists **currently live** sessions so the user can re-point the pane at one | After a reboot the list is empty — there is nothing to re-point to |
| Workspace Snapshot (`spa/src/lib/snapshot/`, Settings → Snapshot) | Global, **manually captured** snapshot; rebuilds every session's name + cwd | Not per-tab; requires the user to have remembered to capture; spec §7 explicitly states the agent inside is not restarted |
| `PaneContent` `tmux-session` variant (`spa/src/types/tab.ts:38`) | Stores `hostId`, `sessionCode`, `mode`, `cachedName`, `tmuxInstance` | Carries **no cwd, no agent identity, no resume information** — the root gap |

## 2. Goals / non-goals

### Goals

1. Every `tmux-session` pane continuously records what it would take to
   recreate itself: tmux session name, the cwd the agent was launched in, which
   agent is running, and the exact resume command.
2. A pane whose session is gone offers a **single Rebuild button** driving an
   editable, individually-checkable action set (create session / cwd / resume
   command), all three checked by default.
3. The same three fields are visible and editable from the tab-name
   double-click popover while the session is still alive.
4. Settings → Snapshot becomes a batch view over these per-tab records instead
   of maintaining its own capture.
5. A nested agent (subagent, or a cross-agent proxy such as codex launched
   inside a cc session) must **never** overwrite the pane's record.

### Non-goals

- Restoring scrollback, shell history, or any process other than the agent.
- Stream-mode panes (`mode: 'stream'`). They have their own handoff machinery
  (`internal/module/stream/orchestrator.go`); folding them in would double the
  surface of this spec. Terminal-mode panes only.
- Non-tmux panes (editor / browser / dashboard / settings).
- Cross-device portability of the record. The record lives in the SPA's
  persisted tab store; a different machine or browser has its own tabs. The
  daemon-side cache (§4.5) is a backfill source, not a second source of truth.
- Any new persistence layer. Everything reuses `useTabStore`'s existing
  `persist` and the daemon's existing `session_meta` table.

## 3. Evidence (measured on this machine, 2026-09-07)

### 3.1 What each agent hands us at session start

`pdx hook` forwards the agent's **entire stdin** as `raw_event`
(`cmd/pdx/hook.go:17-26`), so anything the agent puts in its hook payload is
already inside the daemon. It is `DeriveStatus`'s `Detail` allowlist that drops
it before it reaches the SPA.

Two of the three payloads below are **directly observed**, not inferred. The
daemon's trace table `agent_trace_steps` stores every hook request verbatim in
`payload_json` at `kind = 'trigger'` (`internal/store/trace.go:314`), which
makes it the cheapest ground truth available:

```sql
select payload_json from agent_trace_steps
where agent_type = ? and event_name like '%SessionStart%' and kind = 'trigger'
order by created_at desc limit 1;
```

**Claude Code** (observed 2026-09-07, `claude-opus-5[1m]`):

```json
{"tmux_session":"csp","tmux_session_id":"$6","tmux_pane_id":"%6",
 "purdex_name":"PdxSessionStart",
 "raw_event":{"session_id":"441c80d5-…","transcript_path":"/Users/…/441c80d5-….jsonl",
   "cwd":"/Users/wake/Workspace/tangency/csp-plugin","scratchpad_dir":"…",
   "hook_event_name":"SessionStart","source":"startup","model":"claude-opus-5[1m]"},
 "agent_type":"cc","sender_pid":59800,
 "sender_start_time":"Mon Sep  7 15:34:30 2026","sender_uncertain":false}
```

**Codex** (observed 2026-09-07, codex `v0.153.4`, `gpt-6-astra`):

```json
{"tmux_session":"purdex1","tmux_session_id":"$2","tmux_pane_id":"%2",
 "purdex_name":"PdxSessionStart",
 "raw_event":{"session_id":"01a07ace-…","transcript_path":"/Users/wake/.codex/sessions/2026/09/07/rollout-….jsonl",
   "cwd":"/Users/wake/Workspace/wake/purdex/.claude/worktrees/tab-rebuild",
   "hook_event_name":"SessionStart","model":"gpt-6-astra",
   "permission_mode":"bypassPermissions","source":"startup"},
 "agent_type":"codex","sender_pid":93731,
 "sender_start_time":"Mon Sep  7 15:39:06 2026","sender_uncertain":false}
```

| Agent | `session_id` | `cwd` | `source` | Status |
|---|---|---|---|---|
| Claude Code | ✅ | ✅ | ✅ (`startup` / `resume` / `clear` / `compact`) | observed |
| Codex | ✅ | ✅ | ✅ (`startup`) | observed |
| OpenCode | ✅ | ❌ — must be added | n/a | code-read: `internal/agent/opencode/plugin_template.go:83-93`. The plugin template is **ours**, so `cwd` is added there (§4.2) |

Both payloads are wrapped by `pdx hook` with `tmux_session_id`, `tmux_pane_id`,
`sender_pid`, `sender_start_time` and `sender_uncertain` — the last three are
what §4.3's ownership test needs, and they are present and populated.

Codex hooks are installed, pdx-owned and firing on this machine
(`~/.codex/hooks.json` entries all point at `bin/pdx hook --agent codex`;
`~/.config/pdx/logs/pdx.log` shows live `[hook] trigger … agent=codex` lines).
Note the installed codex is `0.153.4` while
`internal/agent/codex/hooks.go:15` still declares
`codexHooksSupportedVersion = "0.124.0"`; the payload shape above is from the
installed version, so Phase 1 codes against what was actually observed.

### 3.2 Resume flags (all three verified by running `--help` locally)

| Agent | Exact resume | cwd-scoped fallback |
|---|---|---|
| Claude Code | `claude --resume <session-id>` | `claude -c` ("continue the most recent conversation in the current directory") |
| Codex | `codex resume <SESSION_ID>` | `codex resume --last` (the picker is cwd-filtered by default; `--all` is what disables cwd filtering) |
| OpenCode | `opencode -s <session-id>` | `opencode -c` |

### 3.3 Nested agents are already handled at the frame layer

`internal/module/agent/frame_ops.go:534-542` — when a `SessionStart` arrives
from a sender that has no frame of its own and a PPID ancestor walk finds a
live cross-type parent in the same pane, the daemon collapses the event into a
**proxy ref on the parent frame** instead of creating a standalone frame. The
comment names the exact case: *"codex spawned from inside a cc session via
codex-companion: cc owns the UX, codex should show as a dot on cc's tab, not as
a separate lit-up frame."*

⚠️ The danger this creates for us: `buildProjectionNormalized`
(`frame_ops.go:1102-1131`) sets `normalized.AgentType` from
`projection.TopFrame.AgentType` — the **owner** — while `normalized.Detail`
still carries whatever the **sender's** `DeriveStatus` produced. A proxied codex
`SessionStart` would therefore broadcast `agent_type: "cc"` alongside codex's
`session_id` and `cwd`. Writing the record off that event would corrupt it.
§4.3 makes the ownership explicit rather than inferring it.

### 3.4 `tmuxInstance` is a declared but unpopulated field

`PaneContent` declares `tmuxInstance: string` (`spa/src/types/tab.ts:38`) but
every construction site passes `''` (`SessionSection.tsx:29,232`,
`SessionsSection.tsx:152,196`, `WorkspaceQuickActionsPopover.tsx:180`,
`WorkspaceQuickCommandsContextMenu.tsx:137`,
`useNotificationDispatcher.ts:365`). Only `SessionPickerList.tsx:51` reads a
real value. Nothing ever compares it.

This matters directly. `useMultiHostEventWs.ts:111-124` marks a pane terminated
when its `sessionCode` is absent from the live session list. But session codes
are a **deterministic, reversible encoding of the tmux id `$N`**
(`internal/module/session/codec.go:22-40`), so after a reboot `$0` mints the
same code again. A pane can therefore find "its" code alive and silently attach
to an **unrelated** session instead of surfacing a Rebuild button. The daemon
already exposes `tmux_instance` = `"<server pid>:<start_time>"` on `/api/info`
(`internal/core/info_handler.go:49`, `internal/config/hostid.go:22-30`), which
changes on every tmux server restart. Populating and comparing it is what makes
the whole feature trigger correctly.

### 3.5 Reusable machinery

- `createSession(hostId, name, cwd, mode)` → returns the created `Session`
  (`spa/src/lib/host-api.ts:100-110`). Daemon side serializes
  `HasSession → NewSession → SetMeta` (`internal/module/session/handler.go:95-108`).
- `executeCommand(hostId, sessionCode, command)` → POSTs `send-keys` with a
  trailing `\n` (`spa/src/lib/execute-command.ts`). Precedent for
  create-then-send with no readiness wait: `internal/module/execution/launcher.go:91-111`.
- `fetchSessionCwd(hostId, code)` → `#{pane_current_path}` (`host-api.ts:157`).
- `session_meta` table already stores per-session `mode` / `cc_session_id` /
  `cc_model` / `cwd`, keyed by tmux id, with orphan rows deleted on lookup miss
  (`internal/store/meta.go:71-79`, `internal/module/session/service.go:102-104`).
- Snapshot engine helpers to be reused in Phase 5: `scanPaneTree`,
  `updatePaneInLayout`, `collectLeaves`, `markTerminated`.

## 4. Design

### 4.1 Data model (SPA, source of truth)

`spa/src/types/tab.ts` — the `tmux-session` variant gains one optional field:

```ts
export interface PaneRebuildRecord {
  /** tmux session name actually in use (including any collision suffix). */
  sessionName: string
  /** cwd the agent was launched in — the cwd its resume is scoped to. */
  cwd?: string
  cwdSource?: 'agent-session-start' | 'pane-probe'
  agent?: {
    type: string          // 'cc' | 'codex' | 'opencode' (open string, mirrors AGENT_NAMES)
    sessionId?: string    // absent when the agent did not report one
    updatedAt: number
  }
  /** Full command line. Composed by §4.7 unless the user overwrote it. */
  resumeCommand?: string
  capturedAt: number
}
```

```ts
| { kind: 'tmux-session'; hostId: string; sessionCode: string;
    mode: 'terminal' | 'stream'; cachedName: string; tmuxInstance: string;
    terminated?: TerminatedReason; rebuild?: PaneRebuildRecord }
```

**Overwrite policy (user decision, 2026-09-07): automatic values always win.**
A user edit is written straight into `rebuild` and persists until the next
qualifying `SessionStart` (§4.3) replaces it. For a dead session no further
`SessionStart` can arrive, so edits made on the Rebuild panel are stable —
which is the case that matters. No `pinned` / sticky flags; there is exactly
one value per field.

The three writers are ranked, and the ranking is the whole of the
concurrency policy:

| Writer | Authority |
|---|---|
| Qualifying `SessionStart` (§4.3) | Overwrites unconditionally |
| User edit (§4.9, §4.10) | Overwrites unconditionally, until the next qualifying `SessionStart` |
| Daemon backfill (§4.5), pane cwd probe (§4.4) | Fills unset fields only — never overwrites |

Per the alpha convention (`feedback_no_alpha_migration`) no persist migration
is written; `rebuild` is optional and absent on existing panes.

### 4.2 Capture pipeline

Three additive steps on an existing pipeline — no new transport.

1. **Daemon, provider layer.** The `PdxSessionStart` branch of
   `deriveCCStatus` (`internal/agent/cc/status.go:14-23`) and
   `deriveCodexStatus` gains
   `Detail: {"session_id": raw["session_id"], "cwd": raw["cwd"]}`, nil-safe,
   in addition to the existing `Status` / `Model`. cc's existing
   `source == "compact"` early-return stays (a compact keeps the same session
   id; there is nothing to update).
   `deriveOpenCodeStatus` gains the same passthrough, and
   `plugin_template.go`'s `session.created` handler is extended to emit
   `cwd` alongside `session_id`.
2. **Daemon, frame layer.** The normalized event carries an ownership flag
   (§4.3).
3. **SPA.** `useAgentStore.handleNormalizedEvent` already receives
   `detail` (`frame_ops.go:1110` → `NormalizedEvent.Detail` →
   `useAgentStore.ts:57-63`). It gains a side effect: when the event qualifies
   (§4.3), call the new `useTabStore.setPaneRebuild(hostId, sessionCode, patch)`
   for every pane bound to that `(hostId, sessionCode)`.

### 4.3 Ownership guard — the anti-mis-attribution invariant

**Invariant.** A `rebuild.agent` / `rebuild.cwd` write happens only for a
`SessionStart` whose sender **is** the pane's top frame after the event is
applied. Every proxy-collapsed and every subagent event is inert for this
record.

The daemon injects `owner_session_start: true` into the broadcast `Detail`
when, and only when, all of the following hold:

- `lifecycle == LifecycleSessionStart`;
- the event was not collapsed into a parent (i.e. neither the
  `frame_ops.go:542` proxy fast-path nor `reconcileCreatedFrameAsProxy`
  claimed it);
- the resulting `projection.TopFrame` is the sender's own frame — matched on
  `AgentType` **and** `(SenderPID, SenderStartTime)`, not on agent type alone;
- `req.SenderUncertain` is false (`cmd/pdx/hook.go:23`). On uncertain
  provenance we keep the previous record rather than risk a wrong one.

The flag is injected at the frame layer, not in `DeriveStatus`, because the
provider cannot know the outcome of the proxy decision. `Detail` reaches the
wire through `buildProjectionNormalized`, which has both the projection and the
`DeriveResult` in hand; the caller additionally holds `req`. The plan pins the
exact injection point.

The SPA's write path tests exactly one thing:
`detail.owner_session_start === true`. It does not re-derive ownership from
`agent_type`, because on a proxied event that field names the owner while the
rest of `detail` describes the sender — the mismatch §3.3 warns about.

### 4.4 What gets recorded, and when

| Trigger | Fields written |
|---|---|
| Qualifying `SessionStart` (§4.3) | `agent.type`, `agent.sessionId`, `agent.updatedAt`, `cwd` (`cwdSource: 'agent-session-start'`), recomposed `resumeCommand`, `capturedAt` |
| Pane attach / session list refresh, when `rebuild.cwd` is still unset (no agent has ever started here) | `cwd` via `fetchSessionCwd` with `cwdSource: 'pane-probe'`; never overwrites an `'agent-session-start'` cwd |
| Session created or renamed through Purdex | `sessionName` |
| User edit in either UI | the edited field verbatim |

A plain shell pane therefore ends up with `sessionName` + `cwd` and no
`agent` / `resumeCommand` — enough to rebuild it as a shell, which is the
correct outcome.

### 4.5 Backfill for events the SPA missed

The SPA is not always running when an agent starts. `session_meta` becomes the
last-known cache:

- add columns `agent_type`, `agent_session_id`, `agent_cwd`, `tmux_instance`;
- the daemon writes them on every qualifying `SessionStart` (§4.3), keyed by
  the session's tmux id, stamping the **current** `tmux_instance`;
- `SessionInfo` (`internal/module/session/provider.go:18-33`) exposes them, and
  therefore `/api/sessions` does;
- provenance is served **only when the stored `tmux_instance` equals the
  current one**. This is what stops a reused `$0` from inheriting the previous
  boot's provenance. On mismatch the row's provenance columns are treated as
  absent (and the existing orphan-cleanup path at
  `internal/module/session/service.go:102-104` still applies).

On `sessions` WS events the SPA fills in any pane field that is currently
unset. It never overwrites a field it already holds — the SPA record stays the
source of truth; this is a gap-filler only.

`cc_session_id` / `cc_model` stay exactly as they are (stream mode owns them,
`internal/module/stream/orchestrator.go:135`). The new columns are the
agent-agnostic terminal-mode path; no behaviour of the existing columns
changes.

### 4.6 Death detection

Two additions, both required for the Rebuild button to appear at the right
moment:

1. **Populate** `tmuxInstance` on every pane whose content is created or
   re-pointed, from `useHostStore` runtime info (`tmux_instance`, already
   fetched per host).
2. **Compare** on each `sessions` WS event: a pane whose recorded
   `tmuxInstance` is non-empty and differs from the host's current one is
   marked `markTerminated(hostId, code, 'tmux-restarted')` regardless of
   whether its code appears alive. Panes with an empty recorded instance
   (legacy panes) keep today's code-absent behaviour, so nothing regresses.

`'tmux-restarted'` already exists as a `TerminatedReason` with copy
(`TerminatedPane.tsx:16`); it is currently only produced by snapshot restore.

### 4.7 Resume command composition

Composed at record-write time and stored as a plain string, so the user always
sees and edits exactly what will run.

| Agent | With `sessionId` | Without |
|---|---|---|
| `cc` | `claude --resume <id>` | `claude -c` |
| `codex` | `codex resume <id>` | `codex resume --last` |
| `opencode` | `opencode -s <id>` | `opencode -c` |
| unknown / none | *(empty — the resume row is disabled and unchecked)* | — |

Original launch flags (`--model`, `--dangerously-skip-permissions`, …) are
**not** reconstructed in this version; the composed command is the minimal
resume. The field is freely editable, which covers the cases that need more.
Recording argv from the process tree is listed as a future extension (§8).

### 4.8 Rebuild engine

`spa/src/lib/rebuild/` (new), pure functions plus one orchestrator, mirroring
the shape of `spa/src/lib/snapshot/`:

```
rebuildPane(hostId, tabId, paneId, plan: { createSession, applyCwd, runResume }) → RebuildReport
```

Steps:

1. **Name collision.** `createSession` is called with the recorded
   `sessionName`; the daemon refuses a duplicate
   (`handler.go:95-108` serializes `HasSession → NewSession`). On collision the
   engine retries with `-2`, `-3`, … up to a small cap, and **the returned
   `Session` object is authoritative** for the name actually used (the same
   rule Workspace Snapshot settled on for §8.1 of its spec).
2. **cwd** is passed to `createSession`; when the cwd row is unchecked the
   session is created in the daemon's default and no `cd` is sent.
3. **Re-point the pane** to the returned `session.code`, refresh
   `cachedName` / `tmuxInstance`, clear `terminated`, and update
   `rebuild.sessionName`.
4. **Resume command** is sent with `executeCommand` (send-keys + `\n`). Sent
   immediately after creation, matching `launcher.go:91-111`; tmux buffers
   input into the pane before the shell drains it.
5. Any failed step surfaces as an error toast naming the step; steps already
   completed are **not** rolled back (a created session is a real, useful
   session). The report lists what ran.

An unchecked row is skipped entirely. Unchecking "create session" is only
meaningful when the pane is re-pointed at something already live, so that row
is disabled (checked, non-editable) whenever the pane is terminated.

### 4.9 UI — terminated pane

`TerminatedPane.tsx` gains the action set above the existing session picker
(the picker stays: re-pointing at a live session is still a valid escape
hatch).

```
⚠ Session gone (tmux restarted)
  ☑ Create tmux session   [purdex1]                           ← inline edit
  ☑ Working directory     [/Users/wake/Workspace/wake/purdex] ← inline edit
  ☑ Run resume command    [claude --resume 3f8a…]  ⓘ Claude Code · 2h ago
                                                    [ Rebuild ]
```

Inline editing follows `EditableCwdCell` (shipped in alpha.324): a
`committedRef` guard against double submit, `disabled` while busy, and the
`compositionRef` + `isComposing` double check so an IME Enter does not commit
(CJK safety). Those two defects were found by review on that component; the
new one reuses the pattern rather than rediscovering them.

Rows with no value (e.g. no resume command) render disabled and unchecked.

### 4.10 UI — tab-name double-click popover

`RenamePopover` (`spa/src/components/RenamePopover.tsx`, mounted at
`App.tsx:317-326`) currently holds a single session-name input. It gains a
details section below the input showing the same three fields for the tab's
`tmux-session` panes, each editable. Behaviour differences from §4.9:

- the session-name row is the popover's existing rename input, which already
  calls the daemon's rename endpoint — editing it renames the live session, and
  the record follows;
- cwd and resume command edit the record only (nothing is sent to a live
  session);
- for a split tab with several tmux panes, one block per pane, labelled by
  `cachedName`.

The popover's existing `useClickOutside` / Escape / Enter semantics are kept;
the added fields must not hijack the Enter key that submits the rename.

### 4.11 Snapshot section becomes a batch view

Settings → Snapshot stops maintaining a separate captured snapshot and instead
renders the live per-tab records: one row per `tmux-session` pane with the same
four-state health indicator it already has (🟢 live / 🔴 dead-but-rebuildable /
⚠️ structure-only / ⚪ host offline), plus a "Rebuild all" action that runs
§4.8 across every 🔴 row.

`spa/src/lib/snapshot/` keeps its restore-side helpers; `capture.ts` and the
`purdex-workspace-snapshot` storage keys are removed along with the actions
that produced them. This is the phase that must not be started before the
per-tab record is proven in use — see §5.

## 5. Phases

| Phase | Scope | Surface |
|---|---|---|
| **1** | Provenance out of the daemon: `session_id` + `cwd` in `SessionStart` `Detail` for cc / codex / opencode (incl. plugin template `cwd`); `owner_session_start` flag at the frame layer (§4.3); `session_meta` columns + `tmux_instance` guard + `/api/sessions` exposure (§4.5) | daemon |
| **2** | `PaneRebuildRecord` type, `setPaneRebuild` store action, WS write path, backfill from `/api/sessions`, `tmuxInstance` populate + mismatch detection (§4.6) | spa |
| **3** | Resume-command composer (§4.7), rebuild engine (§4.8), `TerminatedPane` action set (§4.9) | spa |
| **4** | `RenamePopover` details section (§4.10) | spa |
| **5** | Snapshot section rewritten as a batch view over the records; retire `capture.ts` and its storage keys (§4.11) | spa |

Phases 1–3 are the feature. 4 and 5 are independently shippable and each is a
separate PR.

## 6. Testing strategy

TDD per project convention; each task is a red-then-green commit.

**Phase 1 (Go).**
- `deriveCCStatus` / `deriveCodexStatus` / `deriveOpenCodeStatus`: `SessionStart`
  emits `session_id` + `cwd`; missing keys stay absent rather than becoming
  `nil` entries; cc `source: "compact"` still returns `Valid: false`.
- Ownership flag: table test over (own frame) / (proxy-collapsed) /
  (subagent) / (`SenderUncertain: true`) — flag present only in the first.
  The proxy case must assert the codex-inside-cc shape from §3.3 explicitly.
- `session_meta`: provenance round-trips; a row whose stored `tmux_instance`
  differs from the current one reports no provenance.
- The opencode plugin template already has contract + bun integration tests
  (`plugin_template_contract_test.go`, `..._bun_integration_test.go`); the
  `cwd` addition extends those fixtures.

**Phase 2 (Vitest).**
- Write path: qualifying event writes every pane bound to `(hostId, code)`;
  non-qualifying events (no flag) write nothing; a `pane-probe` cwd never
  overwrites an `agent-session-start` cwd.
- Backfill fills only unset fields.
- `tmuxInstance` mismatch marks `'tmux-restarted'` **even when the code is
  present in the live list** (the reused-`$0` regression), and an empty
  recorded instance preserves today's behaviour.

**Phase 3 (Vitest).**
- Composer: 3 agents × (id / no id) + unknown agent.
- Engine: collision retry uses the returned session's real name; unchecked rows
  are skipped; a failure mid-way reports the completed steps and does not roll
  back; the pane is re-pointed to the new code with `terminated` cleared.
- `TerminatedPane`: default all-checked, IME Enter does not commit, double
  submit guarded, Rebuild disabled while busy.

**Phases 4–5.** Popover: editing cwd does not trigger the rename submit; split
tab renders one block per pane. Snapshot: health states derive from records and
"Rebuild all" hits exactly the 🔴 rows.

Full-suite green + `pnpm run lint` + `pnpm run build` before each PR
(Codex's sandbox has no network — the main session runs these itself, per
`feedback_codex_sandbox_no_install`).

## 7. Limits (to surface in UI copy)

- Only the agent is restored — no scrollback, no shell history, no other
  processes that were running in the session.
- The resume command is the minimal one; extra launch flags are not
  reconstructed. Edit the field if you need them.
- Without an exact session id the fallback resumes *the most recent session in
  that cwd*, which may not be the one this tab had.
- Records live in this app's storage. A different machine or browser profile
  has its own tabs and its own records.
- Stream-mode panes are out of scope in this version.

## 8. Risks / open items

1. **OpenCode is the only unobserved agent.** cc and codex `SessionStart`
   payloads are captured verbatim in §3.1; no opencode session had run recently
   enough to leave a trace row. The opencode path is code-read only
   (`plugin_template.go:83-93`), and it is also the one payload we author
   ourselves, so the risk is confined to whether `cwd` is reachable from the
   plugin's event object. Phase 1 must verify against a real opencode run
   before relying on it; the degradation path (`opencode -c`, §4.7) covers the
   failure.
   Related but out of scope: `internal/agent/codex/hooks.go:15` declares
   `codexHooksSupportedVersion = "0.124.0"` while the installed codex is
   `0.153.4`. Nothing in this feature depends on that constant, but it is stale
   and worth a separate issue.
2. **`owner_session_start` injection point** touches `frame_ops.go`, a large
   and heavily-reviewed file. The plan should keep the change to a single
   well-named helper and not restructure the surrounding flow.
3. **`tmuxInstance` populate is a behaviour change beyond this feature** — it
   makes previously-silent wrong reattachments visible as terminated panes.
   That is the intent (§3.4), but it will change what users see after a reboot
   even before they press Rebuild, so it belongs in the Phase 2 PR description.
4. **Send-keys with no readiness wait** follows existing precedent, but a slow
   shell rc could in principle swallow the buffered line. If observed, add a
   `LooksLikeShellPrompt` poll (`internal/agent/probe/shell_prompt.go:13`)
   before sending. Not designed in now — no evidence it is needed.
5. **Phase 5 deletes `capture.ts` and its storage keys**, retiring part of the
   72-test snapshot suite. It is sequenced last so the replacement is proven
   first; if the per-tab record turns out to have gaps in real use, Phase 5 is
   the one to defer.
