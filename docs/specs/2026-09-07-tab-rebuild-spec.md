# Spec — Per-tab session provenance & one-click rebuild ("Tab Rebuild")

Status: draft v2 (revised after codex spec review R1 `task-mtqxjans-cf1ai3`)
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
| Workspace Snapshot (`spa/src/lib/snapshot/`, Settings → Snapshot) | Global, **manually captured** snapshot; rebuilds every session's name + cwd | Not per-tab; requires the user to have remembered to capture; its spec §7 states the agent inside is not restarted |
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
4. Settings → Snapshot gains a batch view over these records with a
   "Rebuild all".
5. A nested agent — a subagent, a cross-agent proxy (codex inside cc), **or a
   same-type nested agent (cc inside cc)** — must never overwrite the pane's
   record.

### Non-goals

- Restoring scrollback, shell history, or any process other than the agent.
- Stream-mode panes (`mode: 'stream'`). They have their own handoff machinery
  (`internal/module/stream/orchestrator.go`). Terminal-mode panes only.
- Non-tmux panes (editor / browser / dashboard / settings).
- Cross-device portability. The record lives in the SPA's persisted tab store.
- **Daemon-side backfill of records the SPA missed.** Cut from v1 after review
  — see §9.1 for why and what it would take.
- **Retiring `spa/src/lib/snapshot/capture.ts`.** It backs the layout-restore
  and undo paths, which are a different job from session rebuild. See §4.11.
- Multi-agent tmux sessions. A tmux session running agents in several tmux
  panes records only one of them (§4.4); rebuild recreates a single-pane
  session.

## 3. Evidence (measured on this machine, 2026-09-07)

### 3.1 What each agent hands us at session start

`pdx hook` forwards the agent's **entire stdin** as `raw_event`
(`cmd/pdx/hook.go:17-26`), so anything the agent puts in its hook payload is
already inside the daemon. It is `DeriveStatus`'s `Detail` allowlist that drops
it before it reaches the SPA.

Two of the three payloads are **directly observed**. The daemon's trace table
`agent_trace_steps` stores every hook request verbatim in `payload_json` at
`kind = 'trigger'` (`internal/store/trace.go:314`):

```sql
select payload_json from agent_trace_steps
where agent_type = ? and event_name like '%SessionStart%' and kind = 'trigger'
order by created_at desc limit 1;
```

**Claude Code** (observed, `claude-opus-5[1m]`):

```json
{"tmux_session":"csp","tmux_session_id":"$6","tmux_pane_id":"%6",
 "purdex_name":"PdxSessionStart",
 "raw_event":{"session_id":"441c80d5-…","transcript_path":"/Users/…/441c80d5-….jsonl",
   "cwd":"/Users/wake/Workspace/tangency/csp-plugin","scratchpad_dir":"…",
   "hook_event_name":"SessionStart","source":"startup","model":"claude-opus-5[1m]"},
 "agent_type":"cc","sender_pid":59800,
 "sender_start_time":"Mon Sep  7 15:34:30 2026","sender_uncertain":false}
```

**Codex** (observed, codex `v0.153.4`, `gpt-6-astra`):

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

| Agent | `session_id` | `cwd` | Status |
|---|---|---|---|
| Claude Code | ✅ | ✅ | observed |
| Codex | ✅ | ✅ | observed |
| OpenCode | ✅ | ❌ — must be added | code-read: `internal/agent/opencode/plugin_template.go:83-93`. The plugin template is **ours**, so `cwd` is added there (§4.2) |

`pdx hook` wraps every payload with `tmux_session_id`, `tmux_pane_id`,
`sender_pid`, `sender_start_time` and `sender_uncertain` — populated, and what
§4.3 needs.

Installed codex is `0.153.4` while `internal/agent/codex/hooks.go:15` declares
`codexHooksSupportedVersion = "0.124.0"`. Nothing here depends on that
constant; it is stale and gets its own issue.

### 3.2 Resume flags (all three verified by running `--help` locally)

| Agent | Exact resume | cwd-scoped fallback |
|---|---|---|
| Claude Code | `claude --resume <session-id>` | `claude -c` ("continue the most recent conversation in the current directory") |
| Codex | `codex resume <SESSION_ID>` | `codex resume --last` (the picker is cwd-filtered by default; `--all` disables cwd filtering) |
| OpenCode | `opencode -s <session-id>` | `opencode -c` |

### 3.3 Nesting: what the frame layer does and does not protect

`frame_ops.go:534-542` — a `SessionStart` from a sender with no frame of its
own, whose PPID walk finds a live **cross-type** frame in the same pane, is
collapsed into a proxy ref on that parent. The comment names the case:
*"codex spawned from inside a cc session via codex-companion: cc owns the UX,
codex should show as a dot on cc's tab, not as a separate lit-up frame."*
Production data confirms it — an `agent_frames` row for pane `%1`
(`agent_type: cc`) carries
`{"id":"proxy:codex:67658:…","is_proxy":true,"source_turn_id":"01a07a36-…"}`.

**But three holes make "is the top frame" the wrong ownership test** (all
verified against the code during review R1):

1. **Same-type nesting is not proxied.** `frame_ops.go:2008` hard-stops the
   walk and returns `(nil, nil)` when the live ancestor has the *same* agent
   type, so a cc launched inside a cc creates its **own** frame
   (`frame_ops.go:810`), and `projection.go:110-119` picks the frame with the
   greatest `StartedAt` as top. The nested cc becomes top frame.
2. **The walk only sees ancestors that already have a frame.**
   `findProxyParent` (`frame_ops.go:1972-2030`) looks up
   `frames.FindByPanePID`. If the child's `SessionStart` is processed before
   the parent has a frame — daemon restart with process-tree recovery
   (`frame_ops.go:618`, which builds the frame from the *request's* type/PID),
   or plain event ordering — the child looks parentless. `sweep.go:112` repairs
   the frames later but cannot un-write a record.
3. **`(nil, nil)` is overloaded.** No-ancestor, same-type stop, stale
   candidates, depth exceeded and *read errors* all return the same value, so
   a caller cannot distinguish "definitely root" from "could not tell".

### 3.4 Frames are per tmux **pane**; SPA panes bind to a tmux **session**

`agent_frames.pane_id` is a tmux pane id (`%N`), and `handler.go:443` widens
the pane projection to a session-level winner via `projectionForSession`, which
picks among the panes of that tmux session
(`frame_ops.go:1170-1182 selectSessionProjection`). An SPA pane, meanwhile,
binds to a `sessionCode`, i.e. a whole tmux session
(`spa/src/types/tab.ts:38`).

Consequence: the record is a **per-tmux-session** fact assembled from
**per-tmux-pane** events. §4.4 states the resolution rule explicitly instead of
assuming 1:1.

### 3.5 `tmuxInstance` is declared, never written, never read meaningfully

`PaneContent` declares `tmuxInstance: string` (`spa/src/types/tab.ts:38`) but
every construction site passes `''`. The only reader,
`SessionPickerList.tsx:51`, reads `runtime[hostId]?.info?.tmux_instance` — and
**nothing ever populates `runtime.info`**: all six `setRuntime` call sites
(`useMultiHostEventWs.ts:82,97,148,198,203`, `OverviewSection.tsx:145`) pass
only `status` / `latency` / `daemonState` / `tmuxState` / `manualRetry`.
`fetchInfo` results live in `OverviewSection`'s component state. So the field
is empty end to end.

This matters. `useMultiHostEventWs.ts:111-124` marks a pane terminated when its
`sessionCode` is absent from the live list, but session codes are a
deterministic, reversible encoding of the tmux id `$N`
(`internal/module/session/codec.go:22-40`), so after a reboot `$0` mints the
same code. A pane can find "its" code alive and silently attach to an
**unrelated** session. `tmux_instance` = `"<server pid>:<start_time>"`
(`internal/core/info_handler.go:49`, `internal/config/hostid.go:22-30`) changes
on every tmux server restart and is the signal that fixes this — but §4.6 has
to *create* its plumbing, not assume it.

Note `GetTmuxInstance()` returns `""` on error or timeout, so "two empty
strings" must never count as a match.

### 3.6 Reusable machinery

- `createSession(hostId, name, cwd, mode)` → returns the created `Session`
  (`spa/src/lib/host-api.ts:100-110`); daemon serializes
  `HasSession → NewSession → SetMeta` (`internal/module/session/handler.go:95-108`).
- `executeCommand(hostId, sessionCode, command)` → `send-keys` + `\n`
  (`spa/src/lib/execute-command.ts`). Create-then-send with no readiness wait
  has precedent at `internal/module/execution/launcher.go:91-111`.
- `fetchSessionCwd(hostId, code)` → `#{pane_current_path}` (`host-api.ts:157`).
- `spa/src/lib/snapshot/restore.ts` already solves session-remap
  (`remapLayoutSessions`), partial-failure reporting (`rebuiltButUnattached`,
  `restore.ts:315,432`) and session-store sync (`restore.ts:342`). §4.8 reuses
  those shapes rather than reinventing them.
- Ancestor identification without frames: `agent.ReadProcessInfo` +
  each provider's `Identify(ProcessInfo)` (`internal/agent/cc/provider.go:46`,
  `codex/provider.go:28`, `opencode/provider.go:28`), the same predicates
  `probe.FirstAliveAgentInTree` uses.

## 4. Design

### 4.1 Data model (SPA, source of truth)

`spa/src/types/tab.ts`:

```ts
export interface PaneRebuildRecord {
  /** tmux session name actually in use (including any collision suffix). */
  sessionName: string
  /** Generation this record describes: the host's tmux_instance at write time. */
  tmuxInstance: string
  /** cwd the agent was launched in — the cwd its resume is scoped to. */
  cwd?: string
  cwdSource?: 'agent-session-start' | 'pane-probe'
  agent?: {
    type: string          // 'cc' | 'codex' | 'opencode' (open string, mirrors AGENT_NAMES)
    sessionId?: string
    /** tmux pane the owning SessionStart came from; see §4.4. */
    tmuxPaneId?: string
    updatedAt: number
  }
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
the case that matters. No `pinned` / sticky flags.

Three writers, ranked, and the ranking is the whole concurrency policy:

| Writer | Authority | Granularity |
|---|---|---|
| Qualifying `SessionStart` (§4.3) | Overwrites | **The whole agent group as one unit**: `agent.*`, `cwd`(+`cwdSource: 'agent-session-start'`), `resumeCommand`, `capturedAt`. Never a partial merge — a payload missing `cwd` clears `cwd` rather than leaving the previous agent's value attached to a new session id |
| User edit (§4.9, §4.10) | Overwrites, until the next qualifying `SessionStart` | Only the field actually edited, applied inside a functional `set` against the latest state |
| Pane cwd probe (§4.4) | Fills only when `cwd` is unset | `cwd` (+`cwdSource: 'pane-probe'`) |

A `'pane-probe'` cwd is never promoted to agent provenance and is always
replaced wholesale by the next agent group write.

Per the alpha convention (`feedback_no_alpha_migration`) no persist migration
is written; `rebuild` is optional and absent on existing panes.

### 4.2 Capture pipeline

1. **Daemon, provider layer.** The `PdxSessionStart` branch of
   `deriveCCStatus` (`internal/agent/cc/status.go:14-23`),
   `deriveCodexStatus` and `deriveOpenCodeStatus` adds
   `Detail: {"session_id": …, "cwd": …}`, nil-safe. cc's existing
   `source == "compact"` early-return stays (a compact keeps the same session
   id). `plugin_template.go`'s `session.created` handler additionally emits
   `cwd`.
2. **Daemon, frame layer.** One ownership decision per event (§4.3) produces
   the `owner_session_start` flag.
3. **SPA.** `useAgentStore.handleNormalizedEvent` already receives `detail`
   (`frame_ops.go:1110` → `useAgentStore.ts:57-63`) and gains a side effect:
   on a qualifying event, write the agent group into every pane matching
   `(hostId, sessionCode, tmuxInstance)` (§4.5).

### 4.3 Ownership — ancestry-verified, not projection-derived

**Invariant.** The record is written only by a `SessionStart` whose sender has
**no live agent ancestor inside the same tmux pane**, of any agent type. Being
the top frame is neither sufficient (§3.3 hole 1) nor necessary (§3.3 hole 2)
and is not used.

A new classifier, extracted so the proxy decision and the ownership decision
share one traversal:

```go
type PaneAncestry int
const (
    AncestryRoot      PaneAncestry = iota // no agent ancestor in this pane
    AncestrySameType                      // a live ancestor of the same agent type
    AncestryCrossType                     // a live ancestor of a different agent type
    AncestryUnknown                       // could not be determined
)

func (m *Module) classifyPaneAncestry(req EventRequest) (PaneAncestry, *store.Frame, error)
```

- Walks the sender's PPID chain, capped at the existing `proxyMaxDepth`,
  bounded to the same `TmuxPaneID`.
- At each ancestor it reads `agent.ReadProcessInfo(pid)` and asks every
  registered provider's `Identify(proc)`. **It does not require the ancestor to
  have a frame** — that is what closes §3.3 hole 2 (child-first ordering,
  daemon recovery, sweep transients). When the ancestor does have a frame it is
  returned, so `findProxyParent`'s existing cross-type behaviour is preserved
  by re-expressing it in terms of this classifier.
- Any `ReadProcessInfo` / start-time read failure → `AncestryUnknown`. Depth
  exceeded with no match → `AncestryUnknown`, not `AncestryRoot`.

`owner_session_start: true` is injected into the broadcast `Detail` when **all**
hold:

- `lifecycle == LifecycleSessionStart`;
- `classifyPaneAncestry(req) == AncestryRoot`;
- `req.SenderUncertain == false`. This is a defence-in-depth check, not the
  ownership proof — `verify.go:40` already rejects uncertain senders upstream.

Otherwise the flag is absent and the SPA keeps whatever it had. **Unknown never
writes**; a missed record degrades to `claude -c` / `codex resume --last`
(§4.7), a wrong record does not degrade — it misleads.

The classifier's verdict, the flag, the `Detail` payload, the broadcast
`AgentType` and (if any) DB write must all come from **one** evaluation,
carried on the request as it flows through the handler. They must not be
re-derived from a second `projectionForSession` call
(`handler.go:443`) — that call can select a different tmux pane's projection
(`frame_ops.go:1170-1182`) and would reintroduce exactly the identity/payload
mismatch this section exists to prevent.

The SPA's write path tests exactly one thing:
`detail.owner_session_start === true`. It never re-derives ownership from
`agent_type`.

### 4.4 What gets recorded, and when

| Trigger | Fields written |
|---|---|
| Qualifying `SessionStart` (§4.3) | The whole agent group (§4.1), with `agent.tmuxPaneId` from `req.TmuxPaneID` |
| Pane attach / session list refresh, when `rebuild.cwd` is unset | `cwd` via `fetchSessionCwd`, `cwdSource: 'pane-probe'` |
| Session created or renamed through Purdex | `sessionName` |
| User edit | the edited field |

**Multi-pane tmux sessions.** Several tmux panes in one tmux session can each
run a root agent. The record holds one agent group; the **most recent
qualifying `SessionStart` wins**, and `agent.tmuxPaneId` records which pane it
came from so the UI can say so. Rebuild recreates a single-pane session running
that agent. This is a stated limit (§7), consistent with the user's
"automatic values always win" decision.

A plain shell pane ends up with `sessionName` + `cwd` and no `agent` /
`resumeCommand` — enough to rebuild it as a shell, which is correct.

### 4.5 Generation guard (SPA)

A pane's binding is `(hostId, sessionCode, tmuxInstance)`, not
`(hostId, sessionCode)`. Session codes are reused across tmux server restarts
(§3.5), so **every** SPA writer takes the generation as part of its match:

- the `SessionStart` write path;
- the cwd probe;
- `updateSessionCache` (name refresh);
- termination marking.

`markTerminated(hostId, sessionCode, reason)` (`useTabStore.ts:426`) marks
every pane with that pair and would kill a *new* pane that has legitimately
attached to the reused code. It is replaced for this flow by a
generation-scoped variant — `markTerminatedForGeneration(hostId, sessionCode,
expectedTmuxInstance, reason)` — leaving the existing action in place for
today's `session-closed` / `host-removed` callers. Panes whose recorded
instance is empty (legacy panes, or a host whose instance is unknown) are
matched by the old rule so nothing regresses.

Ordering: **classify generation first, then update anything.** Today
`useMultiHostEventWs.ts:106` refreshes names before deciding what died; the new
path decides first.

### 4.6 Instance acquisition and death detection

`tmux_instance` has no producer today (§3.5), so this feature creates one:

1. **Fetch.** The per-host connection flow calls `fetchInfo(hostId)` on connect
   and on every reconnect, and stores the result through
   `setRuntime(hostId, { info })`. `OverviewSection`'s local copy stays as-is;
   it is not the source.
2. **Refresh.** Re-fetch on the `tmux` WS event (`useMultiHostEventWs.ts:147`)
   so a tmux restart under a running daemon is picked up.
3. **Populate.** Panes record the host's current instance when their content is
   created or re-pointed.
4. **Compare.** On each `sessions` event, a pane is marked
   `'tmux-restarted'` **only when both** the recorded and the current instance
   are non-empty **and** they differ. Empty-vs-empty, empty-vs-value and
   value-vs-empty all mean "unknown" and never mark anything dead — that covers
   first load, offline hosts, `GetTmuxInstance()` timeouts, host re-add / undo,
   and a daemon-only restart (where the instance is unchanged, so nothing is
   marked).

`'tmux-restarted'` already exists as a `TerminatedReason` with copy
(`TerminatedPane.tsx:16`).

⚠️ This is a behaviour change beyond the feature: reattachments that are
silently wrong today become visible terminated panes. That is the intent, and
it belongs in the Phase 2 PR description.

### 4.7 Resume command composition

Composed when the agent group is written, stored as a plain string, so the user
sees and edits exactly what will run.

| Agent | With `sessionId` | Without |
|---|---|---|
| `cc` | `claude --resume <id>` | `claude -c` |
| `codex` | `codex resume <id>` | `codex resume --last` |
| `opencode` | `opencode -s <id>` | `opencode -c` |
| unknown / none | *(empty — the resume row is disabled and unchecked)* | — |

Original launch flags are not reconstructed; the field is editable, which
covers the cases that need more (§9.2).

### 4.8 Rebuild engine

`spa/src/lib/rebuild/` (new), pure functions plus one orchestrator, mirroring
`spa/src/lib/snapshot/`:

```ts
rebuildPane(
  hostId, tabId, paneId,
  plan: { createSession: boolean; applyCwd: boolean; runResume: boolean },
): Promise<RebuildReport>

interface RebuildReport {
  hostId: string
  created?: { code: string; name: string }   // survives every later failure
  steps: { create: StepResult; resume: StepResult }  // 'ok' | 'skipped' | { error }
  repointed: boolean
}
```

**Preconditions.** The engine resolves the host explicitly and refuses to run
when it is absent or unreachable. `hostFetch` → `getDaemonBase`
(`useHostStore.ts:143`) silently falls back to the **active host** for an
unknown `hostId`, so a `host-removed` pane would otherwise create the session
and fire the resume command on a different machine. Rebuild is disabled for
`terminated === 'host-removed'` with copy telling the user to restore the host
first.

**Steps.**

1. **Create.** `createSession(hostId, recordedName, cwd, 'terminal')`. On the
   daemon's duplicate-name response — and only that response, matched
   explicitly, not "any failure" — retry `name-2`, `name-3`, up to a fixed cap
   (5), then fail. The returned `Session` is authoritative for the name
   actually used.
2. **cwd.** Passed to `createSession`. When the cwd row is unchecked the
   session is created in the daemon default and no `cd` is sent.
3. **Re-point.** Before mutating, re-read the pane and verify it still holds
   the binding the rebuild started from; if the user closed or re-pointed it
   meanwhile, stop and report `repointed: false` (the created session is named
   in the report, not lost). Otherwise update `sessionCode`, `cachedName`,
   `tmuxInstance`, `rebuild.sessionName`, clear `terminated`, and upsert the
   new `Session` into `useSessionStore` the way `restore.ts:342` does.
4. **Resume.** `executeCommand` (send-keys + `\n`), immediately after creation
   per `launcher.go:91-111`.

**Partial failure.** No rollback — a created session is a real, useful session.
The report carries what was created and which step failed; the pane keeps
showing the Rebuild panel with the completed rows checked-and-disabled and a
**"Retry resume"** action, so a send-keys failure does not force the user to
create a second session. `terminated` is cleared only in step 3, so the panel
does not vanish before the resume result is known
(`SessionPaneContent.tsx:71` swaps the view on that field).

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

Inline editing follows `EditableCwdCell` (alpha.324): `committedRef` against
double submit, `disabled` while busy, and `compositionRef` + `isComposing` so
an IME Enter does not commit (CJK safety). Both were review findings on that
component; the new one reuses the pattern rather than rediscovering them.

Rows with no value render disabled and unchecked. "Create tmux session" is
checked and non-editable-off while the pane is terminated (unchecking it is
only meaningful when re-pointing at something live, which the picker does).

### 4.10 UI — tab-name double-click popover

Today the popover has a single rename input (`RenamePopover.tsx`, mounted at
`App.tsx:317-326`), and its entry point only considers the tab's **primary**
pane (`spa/src/features/workspace/hooks.ts:97`) — it does not open at all when
the primary pane is an editor or a terminated session, even if another pane in
the tab is a live terminal, and `hooks.ts:173` renames a single target.

So this is an entry-point change, not just a content change:

- collect **all** `tmux-session` panes from the tab's layout (`collectLeaves`)
  and render one block per pane, labelled by `cachedName`;
- each block carries its own pane/session target;
- a **live** session's name row goes through the daemon rename that exists
  today; a **dead** pane's name row edits `rebuild.sessionName` only;
- cwd and resume rows edit the record only, on live and dead panes alike;
- the popover opens whenever the tab has at least one `tmux-session` pane.

Existing `useClickOutside` / Escape / Enter semantics are kept, and the added
fields must not hijack the Enter that submits the rename.

### 4.11 Snapshot section — batch view, capture retained

Settings → Snapshot gains a table over the live per-tab records — one row per
`tmux-session` pane, reusing the four-state health indicator it already has
(🟢 live / 🔴 dead-but-rebuildable / ⚠️ structure-only / ⚪ host offline) — plus
**Rebuild all** over the 🔴 rows.

`capture.ts`, the `purdex-workspace-snapshot` keys and the layout-restore /
undo actions **stay**. Review R1 showed `restore.ts:8,445,470` depends on
capture for the `-prev` backup and undo, and the old capture deliberately
includes stream panes (`capture.ts:20`) which this feature excludes. The two
features are separated by job, not merged:

- **layout restore / undo** — the existing snapshot pair, unchanged;
- **session rebuild** — per-tab records, the single source for anything that
  creates sessions or runs resume commands.

"Rebuild all" dedupes by `(hostId, tmuxInstance, sessionCode)` before doing
anything: two panes pointing at the same dead session must produce **one**
created session (both panes re-pointed to it), never `name` plus `name-2` with
the resume command run twice. Batch and single-pane rebuild are mutually
exclusive while either is running.

## 5. Phases

| Phase | Scope | Surface |
|---|---|---|
| **1** | `classifyPaneAncestry` + `owner_session_start`, expressed as one decision per event; `findProxyParent` re-expressed on top of it with behaviour pinned by its existing tests | daemon |
| **2** | `session_id` + `cwd` in `SessionStart` `Detail` for cc / codex / opencode, incl. plugin-template `cwd` | daemon |
| **3** | Instance plumbing: `fetchInfo` → `setRuntime({info})` on connect / reconnect / tmux event; pane `tmuxInstance` populate; generation-scoped termination (§4.5, §4.6) | spa |
| **4** | `PaneRebuildRecord`, `setPaneRebuild`, WS write path, cwd probe, **and the resume composer** (§4.7 ships with the capture that stores its output, per review R1 finding 12) | spa |
| **5** | Rebuild engine (§4.8) + `TerminatedPane` action set (§4.9) | spa |
| **6** | Popover entry-point rework + per-pane blocks (§4.10) | spa |
| **7** | Snapshot batch view + Rebuild all with dedupe (§4.11) | spa |

Phase 1 is deliberately alone: it changes ownership semantics in
`frame_ops.go`, the most heavily-reviewed file in the daemon, and its risk
profile is nothing like the payload passthrough in Phase 2. Phases 1–5 are the
feature; 6 and 7 are independently shippable.

## 6. Testing strategy

TDD per project convention; each task is a red-then-green commit.

**Phase 1 (Go).** Table test over `classifyPaneAncestry`:
root / same-type ancestor (**cc inside cc**) / cross-type ancestor (**codex
inside cc**, the shape observed in `agent_frames`) / ancestor alive but
frameless (child-first ordering) / `ReadProcessInfo` failure / depth exceeded —
asserting `AncestryUnknown` for the last two and `owner_session_start` present
only for root. Plus: `SenderUncertain: true` never gets the flag; the outer
agent's clear/resume still qualifies while a nested child is alive; existing
`findProxyParent` tests stay green through the refactor.

**Phase 2 (Go).** `SessionStart` emits `session_id` + `cwd` for all three
providers; missing keys stay absent rather than becoming nil entries; cc
`source: "compact"` still returns `Valid: false`. The opencode `cwd` addition
extends the existing `plugin_template_contract_test.go` and bun integration
fixtures — and must be verified against a real opencode run (§9.3).

**Phase 3 (Vitest).** `runtime.info` is populated on connect and refreshed on
the tmux event; mismatch marks `'tmux-restarted'` **even when the code is
present in the live list** (the reused-`$0` regression); every
empty-instance combination marks nothing; the generation-scoped mark does not
touch a sibling pane legitimately bound to the same reused code.

**Phase 4 (Vitest).** Qualifying events write every pane matching the full
triple and no pane of another generation; non-qualifying events write nothing;
the agent group is replaced as a unit (a payload without `cwd` does not leave
the previous cwd attached to a new session id); a `'pane-probe'` cwd never
overwrites an `'agent-session-start'` one; a user edit applied inside a
functional update touches only the edited field. Composer: 3 agents × (id / no
id) + unknown.

**Phase 5 (Vitest).** Collision retry fires only on the duplicate-name
response and stops at the cap; a `host-removed` pane cannot rebuild
(explicitly: no request is issued against the active host); re-point is skipped
when the pane's binding changed mid-flight; a send-keys failure keeps the panel
with a working "Retry resume"; the report names the created session on every
failure path; `useSessionStore` gets the new session.

**Phases 6–7.** Popover opens for a tab whose primary pane is an editor but
which contains a terminal pane; one block per pane with independent targets;
editing cwd does not submit the rename. Batch: two panes on one dead session
produce exactly one `createSession` and one resume; batch and single rebuild
are mutually exclusive.

Verification commands (the root `package.json` has no `lint` / `build`
scripts):

```
pnpm --prefix spa exec vitest run
pnpm --prefix spa run lint
pnpm --prefix spa run build
go test ./...
```

Codex's sandbox has no network, so the main session runs these itself
(`feedback_codex_sandbox_no_install`).

## 7. Limits (to surface in UI copy)

- Only the agent is restored — no scrollback, no shell history, no other
  processes.
- The resume command is the minimal one; extra launch flags are not
  reconstructed. Edit the field if you need them.
- Without an exact session id the fallback resumes *the most recent session in
  that cwd*, which may not be the one this tab had.
- A tmux session running agents in several tmux panes records only the most
  recent one; rebuild recreates a single-pane session.
- Records live in this app's storage. Another machine or browser profile has
  its own tabs and its own records.
- An agent started while Purdex was closed leaves no record (§9.1).
- Stream-mode panes are out of scope.

## 8. Review R1 disposition

Codex spec review `task-mtqxjans-cf1ai3` raised 13 findings (5 Blocker). Five
claims were independently verified against the code before revising; all five
held. Disposition:

| # | Finding | Disposition |
|---|---|---|
| 1 | TopFrame is not ownership (same-type nesting) | **Fixed** — §4.3 ancestry classifier |
| 2 | Recovery / child-first / sweep can hand a nested sender the flag | **Fixed** — classifier is frame-independent; `Unknown` never writes (§4.3) |
| 3 | Ownership and broadcast may use different projections | **Fixed** — single decision carried on the request; §3.4 states the pane-vs-session mapping; §4.4 states the multi-pane rule |
| 4 | `session_meta` backfill needs atomic upsert / clear semantics | **Cut** — backfill removed from v1 (§9.1) |
| 5 | §4.6 runtime info source does not exist | **Fixed** — §4.6 creates the plumbing; empty instances never mark dead |
| 6 | Instance guard did not protect SPA writers; `markTerminated` over-reaches | **Fixed** — §4.5 generation guard + scoped mark |
| 7 | Writer ranking allows inconsistent field mixes | **Fixed** — §4.1 agent group is written as one unit; probe cwd never promoted |
| 8 | Rebuild can hit the wrong host | **Fixed** — §4.8 preconditions, `host-removed` blocked |
| 9 | Partial success has no recoverable state | **Fixed** — §4.8 report + Retry resume + binding check before re-point |
| 10 | "Rebuild all" duplicates sessions | **Fixed** — §4.11 dedupe by `(hostId, tmuxInstance, sessionCode)` |
| 11 | Popover entry point insufficient for split tabs | **Fixed** — §4.10 is now an entry-point change |
| 12 | Phase dependencies; snapshot retirement not closed | **Fixed** — composer moved in with capture (Phase 4); phases split to 7; §4.11 retains capture and separates the two jobs |
| 13 | Verification commands wrong | **Fixed** — §6 |

## 9. Deferred / risks

### 9.1 Daemon-side backfill (cut from v1)

Records are only written while the SPA is running. An agent started with Purdex
closed leaves the pane without provenance; it degrades to the cwd-scoped
fallback (§4.7) once a cwd probe lands.

The v1 design routed backfill through `session_meta`, and review R1 finding 4
showed that is not a small addition: `meta.go:137` is UPDATE-only and external
sessions may have no row; `SetMeta` writers (`handler.go:129,271`) would need
to preserve the new columns; nil-means-no-change would let a previous
generation's id survive under a fresh instance stamp; and orphan cleanup does
not fire for a reused id (`meta.go:178,193`, `service.go:75`). Doing it right
needs a provenance-specific atomic upsert with whole-group replace-or-clear
semantics and both stamps required non-empty. That is its own spec.

### 9.2 Launch-flag reconstruction

`ProcessInfo.Argv` is already available (`internal/agent/process_info.go:14-20`)
and would let the composer reproduce `--model`, `--dangerously-skip-permissions`
and friends. Not in v1: argv reflects the *original* launch, not the current
state, and the editable field covers the need.

### 9.3 OpenCode is the only unobserved agent

cc and codex payloads are captured verbatim (§3.1); no opencode session had run
recently enough to leave a trace row. The opencode path is code-read only, and
it is also the payload we author ourselves, so the risk is confined to whether
`cwd` is reachable from the plugin's event object. Phase 2 verifies against a
real opencode run before relying on it; `opencode -c` covers the failure.

OpenCode additionally shares a PID between parent and child sessions, which is
why the plugin filters children by `parentID`
(`plugin_template.go:47,82`). §4.3's process-ancestry classifier cannot
distinguish them, so **that provider-level filter is a stated precondition of
the ownership invariant for opencode** and must not be removed without
replacing it.

### 9.4 Send-keys with no readiness wait

Follows existing precedent (`launcher.go:91-111`), but a slow shell rc could
swallow the buffered line. If observed, add a `LooksLikeShellPrompt` poll
(`internal/agent/probe/shell_prompt.go:13`). Not designed in now — no evidence
it is needed, and §4.8's "Retry resume" is the manual escape.
