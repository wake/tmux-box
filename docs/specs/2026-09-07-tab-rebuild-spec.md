# Spec — Per-tab session provenance & one-click rebuild ("Tab Rebuild")

Status: draft v4 (revised after codex spec reviews R1 `task-mtqxjans-cf1ai3`,
R2 `task-mtqxyow8-285jje`, R3 `task-mtqyhkkh-pdsbag` — R3 returned no Blockers)
Date: 2026-09-07
Branch: `worktree-tab-rebuild`

> **v3 reverses v2's central mechanism.** R1 pushed ownership off the frame
> layer and onto process ancestry; R2 showed that trade breaks the common case
> to fix a rare one (§3.3.1). v3 returns ownership to the frame layer with one
> added distinction, and moves every remaining ambiguity onto data the daemon
> already knows rather than data the SPA has to infer.

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

All three payloads are now **directly observed** (opencode was added by the
Task 3 verification gate, 2026-09-07). The daemon's trace table
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

**OpenCode** (observed, opencode `1.18.29`, `openai/gpt-6-astra`) — captured
against the **pre-fix** plugin already installed in `~/.config/opencode/plugins/`,
i.e. this is the `cwd`-absent baseline Task 3 removes:

```json
{"tmux_session":"pdxocverify","tmux_session_id":"$10","tmux_pane_id":"%10",
 "purdex_name":"PdxSessionStart",
 "raw_event":{"session_id":"ses_f84eb36d7fferSh22ZPIw4lt9z"},
 "agent_type":"opencode","sender_pid":21712,
 "sender_start_time":"Mon Sep  7 16:55:05 2026","sender_uncertain":false}
```

Three things this run settles. (1) The plugin does fire, and the `pdx hook`
wrapper fields are populated exactly as for cc/codex. (2) `raw_event` carries
**only** `session_id` — no `cwd`, no transcript path, no model; opencode's
`session.created` Bus event gives the plugin nothing beyond the session id, so
`cwd` genuinely has to come from the plugin's own `PluginInput`, as §4.2
assumes. (3) The session id is opaque and `ses_`-prefixed (not a UUID like the
other two), which is what `opencode -s <session-id>` consumes in §3.2.
`session.created` fires on the first **prompt**, not at TUI launch — creating a
new session from the command palette emitted nothing.

| Agent | `session_id` | `cwd` | Status |
|---|---|---|---|
| Claude Code | ✅ | ✅ | observed |
| Codex | ✅ | ✅ | observed |
| OpenCode | ✅ | ❌ before Task 3 → ✅ after | observed (pre-fix payload above). The plugin template is **ours**, so `cwd` is added there (§4.2): `PurdexOpenCodeHooks` now takes opencode's `PluginInput` and emits `cwd` from `input.directory`, falling back to `input.worktree` then `process.cwd()`. `@opencode-ai/plugin@1.14.19` types confirm `Plugin = (input: PluginInput, options?) => Promise<Hooks>` with `directory: string` and `worktree: string`, so widening the export's signature keeps the loader contract |

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

### 3.3 Nesting: measured exposure

Counted over this machine's `agent_trace_steps` (40 132 rows, `kind='trigger'`
for the event mix, `kind='frame'` for the decisions):

| Fact | Numbers | Consequence |
|---|---|---|
| Subagents emit their own event, never `SessionStart` | cc `PdxSubagentStart` 21 vs `PdxSessionStart` 16; codex 9 vs 270 | **Subagents are structurally out of reach of this feature.** The user's original concern is already handled at the event level, not by anything this spec adds |
| Cross-type nesting is collapsed into a proxy ref | `SessionStart → updated_frame / proxy_subagent_attached` **45 times** | The existing `frame_ops.go:534-572` fast-path is live and heavily exercised. An `agent_frames` row for pane `%1` (`agent_type: cc`) carries `{"id":"proxy:codex:67658:…","is_proxy":true,…}` — codex-companion, exactly as its comment describes |
| Ordinary starts create their own frame | `created_frame` 159 (143 codex + 16 cc), all via `rebuiltMatched` → `reason: daemon_restart_recovery` (`frame_ops.go:866-871`) | That reason name is misleading: it is the normal path for an agent whose frame does not exist yet, not an exceptional one |

So the only real hole is **same-type nesting**: `frame_ops.go:2008` hard-stops
the walk and returns `(nil, nil)` when the live ancestor has the *same* agent
type, so a cc launched inside a cc creates its own frame and — because
`projection.go:110-119` picks the greatest `StartedAt` — becomes top frame.
That is why "is the top frame" is not the ownership test. It is *one*
distinction away from being right, not a wrong layer.

#### 3.3.1 Why v2's process-ancestry classifier was worse (measured)

v2 replaced the frame walk with a PPID walk that identified ancestors via each
provider's `Identify(ProcessInfo)`. Review R2 predicted a launcher
counterexample; it reproduces exactly on this machine:

```
93731  ppid 93730  …/codex-darwin-arm64/vendor/…/bin/codex      ← the agent (hook sender)
93730  ppid 93727  node /Users/wake/.nvm/…/bin/codex app-server ← npm launcher, still alive
```

`bin/codex.js` uses `spawn`, not exec-replace, so the Node launcher stays as
the native binary's parent for the whole session, and
`codex.Identify` matches any JS runtime whose argv contains `/codex/`
(`internal/agent/codex/provider.go:33-36`, pinned by
`codex/provider_test.go:28`). A process-ancestry rule would therefore classify
**every npm-installed codex as nested inside itself** and record nothing —
breaking the primary case to fix a rare one.

Frames do not have this problem **by construction**: a frame is only ever
created for a hook *sender* (`frame_ops.go` keys on `req.SenderPID`), and the
launcher never sends a hook, so it never has a frame. Requiring a frame is
precisely what makes the ancestor test safe.

The residual cost of staying frame-based is R1's finding 2: an ancestor that
has not yet sent its first hook is invisible, so a child-first event ordering
can briefly look parentless. §4.3 accepts that and shows why it is
self-healing.

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

### 4.3 Ownership — frame-based, one distinction added

**Invariant.** The record is written only by a `SessionStart` that **creates or
updates the sender's own frame and has no live, identity-verified agent frame
above it in the same tmux pane** — of any agent type, including its own.

This is the existing walk with its overloaded `(nil, nil)` return split. The
walk at `frame_ops.go:1972-2030` already computes everything needed; it just
throws the distinction away:

```go
type AncestorVerdict int
const (
    VerdictRoot          AncestorVerdict = iota // no live framed agent ancestor
    VerdictSameTypeAbove                        // live, identity-verified same-type ancestor frame
    VerdictProxyParent                          // live cross-type ancestor frame → existing collapse
    VerdictIndeterminate                        // walk could not complete (read error, depth cap)
)

func (m *Module) classifyAncestor(req EventRequest) (AncestorVerdict, *store.Frame, error)
```

`findProxyParent` becomes a thin caller: `VerdictProxyParent` → the frame it
already returned; everything else → `nil`. Behaviour for existing callers is
unchanged by construction, including the stale-frame rule
(`frame_ops.go:1996-2015`: liveness + identity gating applies to same-type and
cross-type alike, so a stale same-type frame keeps the walk going and yields
`VerdictProxyParent` from an outer cross-type ancestor — the regression pinned
by `frame_ops_test.go:1470`). The two accumulators — ownership verdict and
proxy candidate — are collected in one traversal but reported separately, so
neither shadows the other.

Provenance is written only on `VerdictRoot`. `VerdictIndeterminate` never
writes.

**Why requiring a frame is the right call, not a compromise.** A frameless
ancestor is an agent that has not sent its first hook yet. Two things follow:
it has written no record that a child could corrupt, and its own
`SessionStart` is still coming — and when it lands it overwrites, because
automatic values always win (§4.1). The child-first ordering hole is therefore
**self-healing within one event**, whereas dropping the frame requirement
breaks every npm-installed codex permanently (§3.3.1). Frame-based also costs
zero new `ps` calls per event.

`owner_session_start: true` is emitted when **all** hold:

- `lifecycle == LifecycleSessionStart`;
- `classifyAncestor(req) == VerdictRoot`;
- `req.SenderUncertain == false` (defence in depth; `verify.go:40` already
  rejects uncertain senders upstream).

#### 4.3.1 Provenance envelope

The verdict is computed **once**, before the frame mutation — but it is a
*candidacy*, not the final authority. `applyFrameEvent` runs a **second**
proxy attempt after the Upsert (`frame_ops.go:828-845`
`reconcileCreatedFrameAsProxy`), which folds a frame the pre-walk had judged
root; `TestPhase35_IT3_PreWalkMiss_PostReconcileHit`
(`frame_ops_test.go:2535`) pins exactly that sequence. Emitting on the
pre-verdict alone would therefore attach an envelope to an event that ended up
collapsed into a parent (review R3 finding 3).

So the envelope is emitted only when the **mutation outcome** confirms the
sender kept its own frame:

- the pre-walk verdict was `VerdictRoot`, **and**
- the event was not taken by the pre-Upsert proxy fast-path
  (`frame_ops.go:542`), **and**
- `reconcileCreatedFrameAsProxy` did not canonicalize it
  (`canonicalized == false`), **and**
- the resulting frame is the sender's own with `ParentFrameID == ""`.

The pre-walk verdict is a cheap early rejection; the post-Upsert walk is
**not** replaced or short-circuited by it.

The envelope travels inside `Detail`:

```json
"pdx_provenance": {
  "owner_session_start": true,
  "agent_type": "codex",
  "session_id": "01a07ace-…",
  "cwd": "/Users/wake/Workspace/wake/purdex",
  "tmux_pane_id": "%2",
  "tmux_instance": "4471:1788740000"
}
```

Self-contained is the point. `buildProjectionNormalized` overwrites the
event's outer `AgentType` with the **session projection winner**
(`frame_ops.go:1129`), which can be a different tmux pane of the same tmux
session (`frame_ops.go:1170-1182`, and `handler.go:443` recomputes the
projection after the frame mutation). Forcing that outer field back to the
sender would corrupt session-level status semantics, which it correctly
serves. So the two identities coexist and never mix:

| Field | Meaning | Consumer |
|---|---|---|
| outer `agent_type` | which agent the tmux session's status belongs to | existing status / lights UI, unchanged |
| `pdx_provenance.agent_type` | which agent this SessionStart came from | rebuild record only |

The SPA's write path reads **only** the envelope, and tests exactly one thing:
`detail.pdx_provenance?.owner_session_start === true`. It never re-derives
ownership or agent type from the outer field. No handler restructuring is
required — one value computed early, carried on the request, serialized into
`Detail`.

`tmux_instance` in the envelope is the daemon's own answer to "which tmux
generation is this event about" (§4.6), so the SPA never has to guess it from
a `/api/info` response that may not have arrived yet.

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

Every writer takes its generation from the payload that carried its data
(§4.6) — never from ambient state — so there is no window in which a writer
stamps a generation it merely assumed. An async cwd probe re-verifies the
pane's binding when it resolves, and discards its result if the binding
changed.

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

### 4.6 The generation travels with the data, not alongside it

v2 had the SPA fetch `/api/info` and compare against events. Review R2 showed
that cannot be made correct: the `sessions` snapshot is pushed immediately on
subscribe (`internal/module/session/module.go:97`) and
`spa/src/lib/host-events.ts:57` does not await anything, so `sessions` reliably
beats an async `fetchInfo`; and because the watcher only pushes on a hash
change (`watcher.go:92`), a restart missed during that window is never
re-announced. Any design where the SPA supplies the generation has a startup
window in which it supplies the wrong one.

So the daemon supplies it, on the payloads themselves:

1. **`Session` gains `tmux_instance`** (`internal/module/session/provider.go`),
   so every `/api/sessions` response and every `sessions` WS event
   self-describes its generation. Additive field; existing consumers ignore it.
2. **The provenance envelope carries it** (§4.3.1).
3. **The instance is an input to the watcher's change detection, not a value
   cached beside it.** `hashSessions` currently hashes only the marshalled
   session list (`internal/module/session/watcher.go:213-217`), so a tmux
   restart that happens between two ticks and recreates identical
   sessions produces an identical hash and **never broadcasts** — the SPA would
   keep the old generation forever (review R3 finding 1). The instance is
   therefore read on every tick and hashed together with the list:
   `hash = sha256(tmuxInstance, sessions)`. A restart changes the hash even
   when the list is byte-identical, and the broadcast that follows carries the
   new instance.
   Cost is one `tmux display-message` per tick alongside the `list-sessions`
   the tick already runs. `GetTmuxInstance()` returns `""` on error/timeout
   (`internal/config/hostid.go:22-30`); `""` is hashed like any other value, so
   a transient timeout broadcasts "unknown" and the next successful tick
   broadcasts the real value — self-healing without a separate retry rule.
   Edge case: with **zero** sessions before and after a restart there is
   nothing to distinguish and nothing to protect — every pane is already
   marked dead by the code-absence rule.

Death detection then reads only fields that arrived together:

- A pane is marked `'tmux-restarted'` when its recorded instance and the
  instance on the current `sessions` payload are **both non-empty and
  different**. Every combination involving `""` means unknown and marks
  nothing — covering first load, offline hosts, `GetTmuxInstance()` timeouts,
  host re-add / undo, and daemon-only restarts (instance unchanged → nothing
  marked, correctly).
- **Attach gate, per connection epoch — not just at boot.** A `tmux-session`
  pane does not open its terminal WS until a `sessions` payload from the
  **current** host-connection epoch has been processed. A boot-only gate is not
  enough (review R3 finding 2): health recovery flips the host to `connected`
  before host-events reconnects (`useMultiHostEventWs.ts:82`) and
  `useTerminalWs.ts:51` attaches on that status, so a tmux restart that
  happened while the SPA was offline could still let a terminal attach to a
  stranger that reused the code before the fresh list arrives. Each host
  connection carries an epoch; the gate closes on every reconnect and reopens
  when that epoch's first payload lands, and payloads from a superseded epoch
  are dropped.
  An offline host keeps showing its existing offline state — the gate blocks
  attaching, not rendering, and health/event connections are independent of the
  terminal WS, so there is no deadlock.

`'tmux-restarted'` already exists as a `TerminatedReason` with copy
(`TerminatedPane.tsx:16`).

⚠️ Behaviour change beyond the feature: reattachments that are silently wrong
today become visible terminated panes, and terminal attach is gated on the
first session payload. Both are intended; both belong in that phase's PR
description.

#### 4.6.1 Snapshot restore must stamp the generation

`spa/src/lib/snapshot/restore.ts:139` re-points a pane by spreading the old
content and replacing only code and name — carrying the **old**
`tmuxInstance` onto a session that belongs to the new generation, so the very
next reconciliation would mark the freshly restored pane dead. Restore (and
undo) therefore stamp the current instance whenever they re-point. This ships
in the same phase as the generation guard; "snapshot unchanged" is not
achievable and is not claimed.

### 4.7 Resume command composition

Composed when the agent group is written, stored as a plain string, so the user
sees and edits exactly what will run.

| Agent | With `sessionId` | Without |
|---|---|---|
| `cc` | `claude --resume <id>` | `claude -c` |
| `codex` | `codex resume <id>` | `codex resume --last` |
| `opencode` | `opencode -s <id>` | `opencode -c` |
| unknown / none | *(empty — the resume row is disabled and unchecked; Rebuild recreates a shell)* | — |

The composer runs only when an agent group is written, so a pane that never saw
a qualifying `SessionStart` has no resume command at all — it does **not**
silently fall back to `claude -c`, because nothing tells it which agent to run.
§9.1 states that degradation explicitly, and the row is editable so the user
can supply one.

A record whose agent disagrees with the session's current agent type after a
reconnect is flagged **unverified** (§9.1): shown, but unchecked by default and
skipped by "Rebuild all".

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

**Pinned transport.** The engine resolves the host **once**, at operation
start, into an explicit base URL, and every request in the operation goes
through that pinned transport. It never calls `hostFetch`, because
`getDaemonBase` (`useHostStore.ts:143`) silently falls back to the **active
host** for an unknown `hostId` — so a host removed *during* the operation would
send the resume command to a different machine
(`execute-command.ts:4` re-enters `hostFetch` on every call). Before each
request the engine re-checks that the host still exists with the same
ip/port/token identity; on any change it stops and reports, keeping whatever
was already created. Rebuild is disabled up front for
`terminated === 'host-removed'`, with copy telling the user to restore the host
first.

**Steps — resume before re-point.** The order is dictated by the UI lifecycle:
`SessionPaneContent.tsx:70` swaps `TerminatedPane` out the moment `terminated`
is cleared, so clearing it before the resume result exists would unmount the
panel that is supposed to report that result (review R2 finding 6).

1. **Create.** `createSession(hostId, recordedName, cwd, 'terminal')` over the
   pinned transport. Retry `name-2`, `name-3`, … up to a cap of 5 **only on
   HTTP 409**, which the daemon returns specifically for a duplicate name
   (`internal/module/session/handler.go:101-104`, body
   `session already exists: <name>`) and never for validation (400) or create
   failure (500). `createSession` currently discards the status
   (`host-api.ts:107` throws `Error(status + " " + statusText)`), so it gains a
   typed error carrying the status code. The returned `Session` is
   authoritative for the name actually used.
2. **cwd** is passed to `createSession`; an unchecked cwd row creates the
   session in the daemon default and sends no `cd`.
3. **Resume.** `executeCommand` against the **new** session code, over the
   pinned transport. Send-keys does not require the pane to be attached, which
   is why this can precede the re-point (`launcher.go:91-111` sends the same
   way).
4. **Re-point, last.** Re-read the pane and verify it still holds the binding
   the operation started from; if the user closed or re-pointed it meanwhile,
   stop with `repointed: false` — the created session is named in the report,
   not lost. Otherwise update `sessionCode`, `cachedName`, `tmuxInstance` (the
   *new* generation), `rebuild.sessionName`, clear `terminated`, and upsert the
   new `Session` into `useSessionStore` the way `restore.ts:342` does.

**Partial failure.** No rollback — a created session is a real, useful session.
When step 3 fails, step 4 does not run, so the panel stays mounted; it renders
the completed rows checked-and-disabled plus **"Retry resume"** (re-runs step 3
against the already-created session) and **"Attach anyway"** (runs step 4 and
drops the resume). The report — created host/code/name and each step's result —
lives in a rebuild-operation store keyed by `paneId`, not in component state,
so it survives any re-render or remount.

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
undo actions **stay**: `restore.ts:8,445,470` depends on capture for the
`-prev` backup and undo, and the old capture deliberately includes stream
panes (`capture.ts:20`) which this feature excludes.

Review R2 finding 11 is right that "per-tab records are the only thing that
creates sessions" is then false — `undoLastRestore` → `restoreAll` runs with
`rebuild: true` and creates sessions from the snapshot's own `sessionMeta`
(`restore.ts:80,470`). So the boundary is stated by capability, not by
exclusivity:

| Action | Creates sessions? | From what | Runs a resume command? |
|---|---|---|---|
| Snapshot: restore layout | no | — | no |
| Snapshot: rebuild all sessions (legacy) | yes | snapshot `sessionMeta` — **name + cwd only, shell** | **no** |
| Snapshot: restore everything | yes | snapshot `sessionMeta` — name + cwd only, shell | no |
| Snapshot: undo last restore | yes, via the above | snapshot `sessionMeta` | no |
| **Tab Rebuild (new)** | yes | per-tab records | **yes** |

"Restore everything" is the entry the UI calls directly
(`SnapshotSettingsSection.tsx:279` → `restoreAll`, `restore.ts:424`): rebuild
plus layout restore in one action.

Only the new engine ever runs an agent command; the legacy actions remain
shell-only and are labelled as such in the UI. All five take the same
operation lock — shipped in Phase 5, not Phase 7 — so a legacy restore and a
Tab Rebuild cannot interleave, and all five stamp the current generation when
they re-point (§4.6.1). The lock is re-entrant along the undo → restoreAll
path, or acquired only at the outermost entry; the plan picks one.

**Rebuild all** groups panes by `(hostId, tmuxInstance, sessionCode)` — the
instance stays in the key, because the same code under two different non-empty
instances really is two different historical sessions. Within a group:

- one `createSession` and one resume, with **every** pane in the group
  re-pointed to the result (each re-point re-verifies its own binding first);
- when panes in a group carry conflicting hand-edited values, the most recent
  `capturedAt` wins and the UI names the pane it came from before running;
- panes whose recorded instance is `''` (unknown generation) are **excluded
  from the automatic batch** and listed separately as "needs attention" with a
  single-pane Rebuild each — grouping them by code alone is exactly the
  merge-two-different-sessions mistake the key exists to prevent.

Batch and single-pane rebuild are mutually exclusive while either is running.

## 5. Phases

| Phase | Scope | Surface | Shippable alone because |
|---|---|---|---|
| **1** | `classifyAncestor` split out of the existing walk; `findProxyParent` re-expressed on it (§4.3) | daemon | Pure refactor — no caller behaviour changes; existing proxy tests are the acceptance criteria |
| **2** | Provenance envelope: `session_id` / `cwd` / `tmux_pane_id` / `tmux_instance` / `owner_session_start` in `Detail` for cc, codex, opencode (incl. plugin-template `cwd`); `tmux_instance` on `Session` (§4.3.1, §4.6) | daemon | Additive payload fields; nothing consumes them yet |
| **3** | Generation guard: pane `tmuxInstance` populate from payloads, `markTerminatedForGeneration`, bootstrap attach gate, connection epoch, snapshot re-point stamping (§4.5, §4.6, §4.6.1) | spa | Self-contained correctness fix — wrong reattachments become visible terminated panes; no record exists yet, nothing depends on one |
| **4** | `PaneRebuildRecord`, `setPaneRebuild`, envelope write path, cwd probe, **and the resume composer** (R1 finding 12: the composer ships with the capture that stores its output) | spa | Records accumulate and are visible in the popover-less state; no UI depends on them yet |
| **5** | Rebuild engine + operation store + `TerminatedPane` action set, **and the shared operation lock wired into the legacy snapshot actions** (§4.8, §4.9, §4.11) | spa | The user-facing feature; the lock ships here because Snapshot today only has a component-local `busyRef` (`SnapshotSettingsSection.tsx:249`), so without it a legacy restore could replace the whole tab snapshot (`restore.ts:448`) underneath an in-flight rebuild |
| **6** | Popover entry-point rework + per-pane blocks (§4.10) | spa | Pure UI over existing data |
| **7** | Snapshot batch view, Rebuild all with grouping, legacy-action labelling (§4.11) | spa | Pure UI over the records, on top of the engine and lock from Phase 5 |

Phase 1 is deliberately alone: it touches `frame_ops.go`, the most
heavily-reviewed file in the daemon, and its risk profile is nothing like the
payload passthrough in Phase 2. Phase 3 is the one with user-visible behaviour
change independent of the feature, and says so in its PR.

## 6. Testing strategy

TDD per project convention; each task is a red-then-green commit.

**Phase 1 (Go).** `classifyAncestor` table test: no framed ancestor →
`VerdictRoot`; live identity-verified **same-type** ancestor frame (cc inside
cc) → `VerdictSameTypeAbove`; live **cross-type** ancestor frame (codex inside
cc, the shape observed in `agent_frames`) → `VerdictProxyParent`; **stale**
same-type frame above a live cross-type one → still `VerdictProxyParent`
(the regression `frame_ops_test.go:1470` pins); read error / depth cap →
`VerdictIndeterminate`. Behaviour parity: every existing `findProxyParent`
test passes unchanged, and the ownership verdict and proxy candidate are
asserted to be reported independently from one traversal.

**Phase 2 (Go).** The envelope is emitted with `owner_session_start` only for
`VerdictRoot`; absent for same-type-above, proxy-collapsed, and
`SenderUncertain: true`. `session_id` / `cwd` present for all three providers;
missing keys stay absent rather than becoming nil entries; cc
`source: "compact"` still returns `Valid: false`. **A proxied codex
`SessionStart` inside a cc pane broadcasts outer `agent_type: "cc"` and no
envelope** — the identity-mixing case §4.3.1 exists to prevent. `Session`
carries `tmux_instance`, and `""` is propagated on `GetTmuxInstance()` failure
rather than omitted. The opencode `cwd` addition extends
`plugin_template_contract_test.go` and the bun integration fixtures, and must
be verified against a real opencode run (§9.3).

**Phase 3 (Vitest).** An instance mismatch marks `'tmux-restarted'` **even
when the code is present in the live list** (the reused-`$0` regression);
every combination involving `''` marks nothing; the generation-scoped mark
leaves a sibling pane legitimately bound to the same reused code alone; a
terminal does not attach before the host's first `sessions` payload; a payload
from a superseded connection epoch is dropped; snapshot restore and undo stamp
the current instance when re-pointing (§4.6.1), and a restored pane is not
marked dead by the next reconciliation.

**Phase 4 (Vitest).** Envelope-bearing events write every pane matching the
full triple and no pane of another generation; events without the envelope
write nothing; the agent group is replaced as a unit (a payload without `cwd`
does not leave the previous cwd attached to a new session id); a
`'pane-probe'` cwd never overwrites an `'agent-session-start'` one and
re-verifies its binding on resolve; a user edit applied inside a functional
update touches only the edited field. Composer: 3 agents × (id / no id), and
**no agent → no command at all** (not a fallback). Unverified flagging when the
reconnect projection's agent type disagrees with the record.

**Phase 5 (Vitest).** Retry fires only on **HTTP 409** — 400 and 500 must not
trigger a rename retry — and stops at the cap; a `host-removed` pane cannot
rebuild and **no request is issued against the active host**; a host removed
*mid-operation* aborts before the resume instead of sending it elsewhere;
resume runs before re-point, so a send-keys failure leaves the panel mounted
with a working "Retry resume" and "Attach anyway"; re-point is skipped when the
pane's binding changed mid-flight; the report names the created session on
every failure path and survives a remount; `useSessionStore` gets the new
session.

**Phases 6–7.** Popover opens for a tab whose primary pane is an editor but
which contains a terminal pane; one block per pane with independent targets;
editing cwd does not submit the rename. Batch: two panes on one dead session
produce exactly one `createSession` and one resume with **both** panes
re-pointed; panes with an unknown (`''`) instance are excluded from the batch;
conflicting hand-edits resolve to the latest `capturedAt`; unverified records
are skipped; the legacy snapshot rebuild and the new engine cannot run
concurrently.

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
- An agent started while Purdex was closed leaves no agent record, and Rebuild
  recreates a **shell** in the right directory rather than guessing an agent
  (§9.1). Type a command into the resume row to change that.
- A record whose agent no longer matches what the session is running is shown
  as **unverified**: still visible, unchecked by default, skipped by
  "Rebuild all" (§9.1).
- Panes whose tmux generation is unknown are excluded from "Rebuild all" and
  must be rebuilt one at a time (§4.11).
- The legacy Snapshot "rebuild all sessions" and "undo" actions create
  **shells only** — they never run an agent command (§4.11).
- Stream-mode panes are out of scope.

## 8. Review disposition

### 8.0 Review R3 (`task-mtqyhkkh-pdsbag`) — 0 Blocker, 4 Important, 1 Minor

R3 confirmed the frame-ownership reversal and closed 9 of R2's 11 findings.
The five remaining items are all fixed in v4:

| # | Finding | Fix |
|---|---|---|
| 1 | Generation cache cannot be invalidated by session-list change alone — an identical list after a restart never broadcasts (`watcher.go:213-217`) | §4.6: the instance is hashed **with** the list, so a restart always changes the hash; `""` self-heals on the next tick |
| 2 | Attach gate only covered first load; reconnect still races (`useMultiHostEventWs.ts:82`, `useTerminalWs.ts:51`) | §4.6: the gate is per connection epoch and closes on every reconnect |
| 3 | Pre-mutation verdict is not final — `reconcileCreatedFrameAsProxy` folds post-Upsert (`frame_ops.go:828-845`, pinned by `frame_ops_test.go:2535`) | §4.3.1: the envelope is gated on the **mutation outcome**; the pre-walk verdict is only an early rejection and never replaces the post-Upsert walk |
| 4 | Shared lock scheduled for Phase 7 leaves Phase 5-6 racing the legacy restore (`SnapshotSettingsSection.tsx:249`, `restore.ts:448`) | §5: the lock ships **with Phase 5** |
| 5 | Capability table missed "restore everything" (`SnapshotSettingsSection.tsx:279`) | §4.11: added |

### 8.1 Review R2 (`task-mtqxyow8-285jje`) — 5 Blocker, 6 Important

R2's finding 1 is the one that changed the design's direction rather than its
details: it predicted that identifying ancestors by process rather than by
frame would misread an agent's own launcher as a nested agent. That reproduces
exactly (§3.3.1) and would have disabled provenance for every npm-installed
codex. v3 therefore reverses v2's central mechanism instead of patching it,
which also dissolves R2-2.

| # | Finding | Disposition |
|---|---|---|
| 1 | Ancestry classifier cannot prove root; launcher counterexample | **Reversed** — §4.3 returns to frame-based ownership, immune to the launcher by construction (§3.3.1) |
| 2 | One verdict cannot preserve proxy selection | **Dissolved** — the walk is unchanged; only its return value is split, and the two accumulators are reported separately (§4.3) |
| 3 | Provenance vs session-projection identity | **Fixed** — self-contained `pdx_provenance` envelope; the outer `agent_type` keeps its session-level meaning and is never read for the record (§4.3.1) |
| 4 | Events carry no generation | **Fixed** — the daemon stamps `tmux_instance` on the envelope and on `Session`; the SPA never infers it (§4.6) |
| 5 | Bootstrap / reconnect ordering | **Fixed** — generation rides the payload, plus an attach gate and a connection epoch (§4.6) |
| 6 | Clearing `terminated` unmounts the panel | **Fixed** — resume runs before re-point; the operation report lives in a store keyed by `paneId` (§4.8) |
| 7 | Host removed mid-operation | **Fixed** — transport pinned at operation start, identity re-checked per request, no `hostFetch` (§4.8) |
| 8 | Batch: unknown generation and conflicting records | **Fixed** — unknown excluded from the batch, latest `capturedAt` wins, all group panes re-pointed (§4.11) |
| 9 | Degradation promise false; stale records | **Fixed** — never-captured means shell-only; offline-window mismatch is flagged unverified and skipped by batch (§9.1, §4.7) |
| 10 | Snapshot restore re-points with the old generation | **Fixed** — restore and undo stamp the current instance (§4.6.1); "snapshot unchanged" withdrawn |
| 11 | Retained snapshot actions do create sessions | **Fixed** — §4.11 states the boundary by capability, with a shared operation lock; legacy actions are shell-only |
| — | `createSession` discards the 409 | **Fixed** — typed error carrying the status (§4.8) |

### 8.2 Review R1 (`task-mtqxjans-cf1ai3`) — 5 Blocker, 8 Important

Five claims were independently verified against the code before revising; all
five held. R2 then judged 3 of the 12 "Fixed" items closed and 9 partially
closed; the partial ones are folded into §8.1 above.

| # | Finding | Disposition |
|---|---|---|
| 1 | TopFrame is not ownership (same-type nesting) | **Closed in v3** — §4.3 splits the existing walk's return value; v2's ancestry classifier withdrawn (R2-1) |
| 2 | Recovery / child-first / sweep can hand a nested sender the flag | **Accepted, self-healing** — §4.3: a frameless ancestor has written no record, and its own SessionStart overwrites when it lands |
| 3 | Ownership and broadcast may use different projections | **Closed in v3** — §4.3.1 envelope; the outer `agent_type` keeps its session-level meaning |
| 4 | `session_meta` backfill needs atomic upsert / clear semantics | **Cut** — backfill removed from v1 (§9.1) |
| 5 | §4.6 runtime info source does not exist | **Closed in v3** — the SPA no longer needs `runtime.info`; the daemon stamps the generation on the payloads (§4.6) |
| 6 | Instance guard did not protect SPA writers; `markTerminated` over-reaches | **Closed in v3** — §4.5 scoped mark + generation taken from the payload |
| 7 | Writer ranking allows inconsistent field mixes | **Fixed** — §4.1 agent group is written as one unit; probe cwd never promoted |
| 8 | Rebuild can hit the wrong host | **Closed in v3** — §4.8 pinned transport, not just a precondition |
| 9 | Partial success has no recoverable state | **Closed in v3** — §4.8 resume-before-re-point + operation store |
| 10 | "Rebuild all" duplicates sessions | **Closed in v3** — §4.11 grouping, unknown-generation exclusion, conflict rule |
| 11 | Popover entry point insufficient for split tabs | **Fixed** — §4.10 is now an entry-point change |
| 12 | Phase dependencies; snapshot retirement not closed | **Closed in v3** — §5 states why each phase ships alone; §4.11 states the capability boundary |
| 13 | Verification commands wrong | **Fixed** — §6 |

## 9. Deferred / risks

### 9.1 Daemon-side backfill (cut from v1)

Records are only written while the SPA is running, and v2's claim that a missed
capture "degrades to the cwd-scoped fallback" was wrong (review R2 finding 9):
the composer only runs on an agent-group write, and a cwd probe cannot know
which agent to resume. The honest degradation, now specified in §4.7 and §7:

- **Never captured** → the record has `sessionName` + `cwd` and no agent. The
  resume row is empty, disabled and unchecked; Rebuild recreates a **shell** in
  the right directory. The user can type a resume command into the row.
- **Captured, then the SPA was away while the session changed agents** → the
  record still holds the old agent, and nothing marks it wrong. On reconnect
  the projection replay (`internal/module/agent/module.go:564`) delivers the
  session's *current* agent type but no provenance; when that type disagrees
  with the record, the record is flagged **unverified**. An unverified record
  still shows its exact resume command, but the row is unchecked by default,
  the UI says why, and **"Rebuild all" skips unverified exact resumes** rather
  than silently resuming a stale session id.

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

### 9.3 OpenCode was the only unobserved agent — resolved

**Resolved 2026-09-07 (Task 3 verification gate).** A real opencode `1.18.29`
session was run in a tmux pane and its `PdxSessionStart` trigger row read back;
the payload is now quoted in §3.1. It confirmed the plugin fires, that the
wrapper fields are populated, and that `raw_event` carried only `session_id`
before the fix — so `cwd` had to come from the plugin's own `PluginInput`
(`directory`, then `worktree`, then `process.cwd()`), which is what Task 3
implements. What is *not* yet observed end-to-end is the post-fix payload: the
plugin on disk in the user's `~/.config/opencode` is still the pre-fix one and
was deliberately not overwritten. The rendered template is exercised under real
Bun instead (`plugin_template_bun_integration_test.go`), and `opencode -c`
remains the fallback if a live install ever reports no `cwd`.

OpenCode additionally runs parent and child sessions over the same pane and
sender PID, which is why the plugin filters children by `parentID`
(`plugin_template.go:47,97`). No frame- or process-level test downstream can
separate them — they are the same process — so **that provider-level filter is
a stated precondition of the ownership invariant for opencode** and must not be
removed without replacing it. The measured event mix (§3.3) shows the filter
working: subagent lifecycles arrive as `PdxSubagentStart` / `PdxSubagentStop`,
never as `PdxSessionStart`. A Bun runtime assertion now pins that too, so the
filter cannot be dropped silently along with the `cwd` change.

### 9.4 Send-keys with no readiness wait

Follows existing precedent (`launcher.go:91-111`), but a slow shell rc could
swallow the buffered line. If observed, add a `LooksLikeShellPrompt` poll
(`internal/agent/probe/shell_prompt.go:13`). Not designed in now — no evidence
it is needed, and §4.8's "Retry resume" is the manual escape.
