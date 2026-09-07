# Spec — Resume command templates & provenance backfill

**Status:** v2 (v1 reviewed by codex `task-mtre55dl-hnlmvd`: 2 Blocker, 9 Important, 1 Minor — disposition in §10)
**Follows:** `docs/specs/2026-09-07-tab-rebuild-spec.md` (shipped alpha.332)
**Supersedes:** that spec's §9.1 "Daemon-side backfill (cut from v1)"

Two follow-ups to Tab Rebuild. They share one decision — *who is the authority
for the resume command* — so they share one spec, but they are separately
shippable phases.

| | Problem | Fix |
|---|---|---|
| **A** | The composed `claude --resume <id>` calls the wrong executable for a user who launches Claude Code through a shell function | Per-agent command **templates** in Settings + a per-pane override, resolved at display and send time |
| **B** | A rebuild record is written only by a qualifying `SessionStart`, so every session that predates the deploy rebuilds as a bare shell | The SPA **asks** the daemon who owns the pane, whenever it has a record with no agent |

---

## 1. Problem

### 1.1 A — the composed command names the wrong program

`composeResumeCommand` (`spa/src/lib/rebuild/composer.ts`) hardcodes three
command shapes:

```
cc       claude --resume <id>   /  claude -c
codex    codex resume <id>      /  codex resume --last
opencode opencode -s <id>       /  opencode -c
```

The user does not launch Claude Code as `claude`. They launch it through a zsh
function:

```zsh
cld-yolo() {
  printf '…SetUserVar=is_claude=1…'          # iTerm2 status indicator (tmux passthrough)
  /Users/wake/.local/bin/claude --dangerously-skip-permissions "$@"
  printf '…SetUserVar=is_claude=0…'
}
```

A rebuild that sends `claude --resume <id>` therefore resumes the right
conversation with the wrong program: no `--dangerously-skip-permissions`, and
no status indicator. The per-pane field is editable, so the user *can* fix it
one pane at a time — which is exactly the wrong granularity for a setting that
is identical on every pane.

**Not a reason.** An earlier draft argued templates would let the user restore
launch flags such as `--model`. That is wrong and must not resurface in the
implementation or the UI copy: model and permission mode are session state that
`resume` restores on its own (codex's `resume -m` is documented as an override;
`claude --model` is documented as applying "for the current session"; both
agents' hook payloads report a `permission_mode` the daemon never sets). The
two real reasons are **a custom launch command** and, secondarily, **CLI syntax
drift** — a template lets the user follow an upstream flag change without a
Purdex release.

### 1.2 B — records only exist for sessions that started under the new build

`writeProvenanceRecord` (`spa/src/stores/useAgentStore.ts:73`) runs only for an
envelope carrying `owner_session_start`, and `applyFrameEvent` grants that
envelope only on a `SessionStart` (`frame_ops.go:905`). Every tmux session
already running when alpha.332 was deployed therefore has a record with
`sessionName` + probed `cwd` and **no agent** — its resume row is empty and
Rebuild recreates a bare shell. The user has seen this empty field.

---

## 2. Goals / non-goals

### Goals

- A user who launches an agent through a wrapper gets a working Rebuild without
  editing every pane.
- Saving a template can be **verified**: the user finds out at save time that
  the command is resolvable, rather than at rebuild time when the session is
  already gone.
- A session that was running before the feature shipped acquires an agent
  record without the user doing anything.
- Defaults reproduce today's behaviour exactly. A user who configures nothing
  sees no change.
- "What the panel shows is what gets sent" survives (v1 §4.9).

### Non-goals

- Per-host templates. One global set; the *test* runs against a chosen host
  (§4.4). Stated as a limit (§9).
- Reconstructing original launch flags from argv (v1 §9.2, still deferred).
- Templates for anything but the resume command.
- Running the template to verify it. Only the command word is resolved (§4.4);
  executing `claude --resume …` to check it would start an agent.
- A new notion of ownership. §5 reuses the existing frame-layer ancestry walk
  and nothing else.

---

## 3. Evidence (measured on this machine, 2026-09-07)

### 3.1 Every hook event carries the session id, not just `SessionStart`

Read from `~/.config/pdx/agent_events.db`. Since alpha.330 the step-level
`payload_json` is deduplicated and empty, so the payloads live in
`agent_trace_chains.root_payload_json`.

| Agent | Event | `session_id` | `cwd` |
|---|---|---|---|
| cc | `PdxPreToolUse` / `PdxUserPromptSubmit` / `PdxStop` | ✅ | ✅ |
| codex | `PdxPreToolUse` / `PdxUserPromptSubmit` / `PdxStop` | ✅ (+ `turn_id`) | ✅ |
| opencode | `PdxUserPromptSubmit` / `PdxStop` | ✅ | ❌ — §3.3 |

This is what makes B possible at all: the identity is on the wire long after
the `SessionStart` that Purdex missed.

### 3.2 The frame layer already knows who is in the pane

`agent_frames` (`internal/store/frames.go:34`) holds one row per agent process
per tmux pane, with `pid`, `ppid`, `process_start_time`, `parent_frame_id` and
`subagents_json`. `classifyAncestor` (`internal/module/agent/ancestor.go:47`)
walks a PID's ancestry against those rows and reports `VerdictRoot` when no
live, identity-verified frame sits above it in the same pane.

What the rows do **not** hold is the agent's own session id — that has only
ever existed in flight, inside a hook payload. §5.2 adds it.

`SessionProjection.TopFrame` is **not** the owner: `buildPaneProjection`
(`projection.go:111-119`) sorts by `started_at` and takes the **last**, i.e.
the innermost / most recent agent, which is what the lights UI wants. Any
design that reads ownership off `TopFrame` would mis-attribute a nested
same-type session. The ancestry walk is the only ownership answer in this
codebase and this spec adds no second one.

### 3.3 OpenCode's non-`SessionStart` emits omit `cwd`

`internal/agent/opencode/plugin_template.go` calls `pdxCwd()` only for
`PdxSessionStart` (line 108). `PdxStop` (145) and `PdxUserPromptSubmit` (177)
send `session_id` alone. Adding `cwd: pdxCwd()` to those two emits is a
one-line change each.

### 3.4 A shell function is invisible to a non-interactive shell

`cld-yolo` is defined in the user's `~/.zshrc`. It is not on `PATH` and
`command -v cld-yolo` from a non-interactive shell finds nothing. It resolves
only in an interactive shell that has sourced the rc file — which is what the
rebuild engine gets, because it delivers the command with tmux `send-keys`
into the shell tmux started. Any save-time verification must therefore use an
interactive login shell, or it will report a false negative on the very case
this feature exists for.

---

## 4. Design — A: templates

### 4.1 Data model

**Settings (new store, `spa/src/stores/useResumeTemplateStore.ts`).** Modelled
on `useNotificationSettingsStore`: per-agent record, `purdexStorage`,
registered with `syncManager`.

```ts
export interface ResumeTemplatePair {
  /** Used when the record has a usable session id. Should contain `{id}`. */
  exact: string
  /** Used when it does not. */
  fallback: string
}

interface ResumeTemplateState {
  agents: Record<string, ResumeTemplatePair>   // sparse: only customised agents
  getTemplates: (agentType: string) => ResumeTemplatePair | undefined
  setTemplate: (agentType: string, field: 'exact' | 'fallback', value: string) => void
  resetAgent: (agentType: string) => void
}
```

`DEFAULT_RESUME_TEMPLATES` reproduces §1.1's table verbatim:

```ts
{
  cc:       { exact: 'claude --resume {id}', fallback: 'claude -c' },
  codex:    { exact: 'codex resume {id}',    fallback: 'codex resume --last' },
  opencode: { exact: 'opencode -s {id}',     fallback: 'opencode -c' },
}
```

An agent with no default and no stored entry resolves to **empty**, which is
what already makes an unknown agent rebuild as a shell (v1 §4.7).

**Pane record (`spa/src/types/tab.ts`).**

```diff
-  resumeCommand?: string
+  /**
+   * User override for this pane only. Absent means "compose from templates".
+   * Never written automatically; cleared only when the agent identity it was
+   * written against changes (§4.3).
+   */
+  resumeCommandOverride?: string

-  cwdSource?: 'agent-session-start' | 'pane-probe'
+  cwdSource?: 'agent-session-start' | 'agent-backfill' | 'pane-probe' | 'user'
```

`resumeCommand` is **removed**, not repurposed (user decision, 2026-09-07).
Repurposing would silently promote every already-persisted auto-composed string
into an override, pinning every existing record to the old shape so the
template could never apply. Per the alpha convention
(`feedback_no_alpha_migration`) no migration is written. A stale
`resumeCommand` key surviving in persisted state is inert: after this change no
code reads it, and nothing in the snapshot / sync path validates or re-encodes
the record (`snapshot/capture.ts:140`, `snapshot/storage.ts:19`,
`snapshot/restore.ts:146`, `storage/sync.ts:15` all pass the pane content
through unexamined). Any manual edit made before this change is lost — the
accepted cost.

### 4.2 Resolution — three layers, one function

```ts
// spa/src/lib/rebuild/composer.ts
export function resolveResumeCommand(
  record: Pick<PaneRebuildRecord, 'agent' | 'resumeCommandOverride'> | undefined,
  templates: ResumeTemplateLookup,
): string
```

1. `record.resumeCommandOverride` is a non-empty string → return it verbatim.
2. `record.agent?.type` resolves to a template pair → `sessionId` passes
   `SAFE_SESSION_ID` → `exact` with every `{id}` replaced; otherwise
   `fallback`, **used verbatim, `{id}` included if the user put one there**.
3. Otherwise → `''`.

`SAFE_SESSION_ID` (`/^[A-Za-z0-9_-]{1,128}$/`) is retained unchanged and stays
the only thing ever interpolated. An id outside the alphabet degrades to
`fallback`, never to an interpolated command — the property the v1 composer
had, kept. `{id}` is the only placeholder and there is no escape for a literal
brace.

**Every consumer of the old field becomes a call to this function.** The full
list, so the rename cannot be done half-way:

| File | Sites |
|---|---|
| `spa/src/types/tab.ts` | the field; the `RebuildPatch` `field` union |
| `spa/src/stores/useAgentStore.ts` | 94 — stops storing a command |
| `spa/src/stores/useTabStore.ts` | 200 (agent-group write), 209 (`field` patch) |
| `spa/src/components/RebuildActionSet.tsx` | 18 (`RebuildEditableField`), 229, 294, 298 |
| `spa/src/components/RenamePopover.tsx` | 149, 153 |
| `spa/src/components/TerminatedPane.tsx` | wherever it forwards the field |
| `spa/src/lib/rebuild/batch.ts` | 68, 82 |
| `spa/src/lib/rebuild/eligibility.ts` | any read of the field |
| `spa/src/lib/rebuild/engine.ts` | 164 (`publishRefusal`), 498 |
| `spa/src/components/settings/SnapshotSettingsSection.tsx` | 664 |
| fixtures | `useTabStore.rebuild.test.ts`, `RebuildActionSet.test.tsx`, `RenamePopover.rebuild.test.tsx`, `composer.test.ts`, `batch.test.ts`, `engine.test.ts`, `eligibility.test.ts` |

**`useRebuildStore.ts:40`'s `resumeCommand` keeps its name.** It is not the
record field — it is the string an in-flight operation pinned, and renaming it
along with the record field would blur exactly the distinction §4.3 depends on.

### 4.3 Interaction with the existing writer ranking

| Writer | Effect on the resume command |
|---|---|
| Qualifying `SessionStart` | Clears `resumeCommandOverride` **only if the agent identity changed** — a different `agent.type`, or a different `agent.sessionId`. A re-sent `SessionStart` carrying the same id (an idle re-emit) keeps the user's edit. |
| Backfill (§5) | Writes an agent group where there was none, so there is no prior identity and nothing to clear. |
| User edit | Sets `resumeCommandOverride`; an empty submission **clears** it and the row falls back to the template. |
| Template change in Settings | Affects every pane with no override, retroactively, including dead ones. That is the point of the feature. |

The identity test replaces v1's unconditional clear. Unconditional clearing was
defensible for a *composed cache*; for something the UI now calls an override
it is not, because the only real hazard is a verbatim command carrying a
**stale session id**, and that hazard is exactly "the identity changed".

**"What you see is what gets sent" (v1 §4.9)** is preserved by a single rule:
the panel renders `resolveResumeCommand(...)` **only while no operation exists
for the pane**. Once `useRebuildStore` holds an operation, every row — the
live run, the failure footer, "Retry resume" — renders `op.resumeCommand`, the
string the engine pinned at operation start. Otherwise a template edited in
another window would make the panel advertise a command that Retry would not
send.

### 4.4 Save-time verification

**What is verified.** The **command word only** — the first whitespace-
separated token of the template, with `{id}` never substituted.

**Endpoint.** `POST /api/shell/resolve-command`, in the session module
(`internal/module/session/`), which already owns shell and tmux execution.

```
request   { "command": "cld-yolo" }
400       malformed body — missing / non-string / absent `command`
200       { "resolved": true,  "kind": "function", "detail": "cld-yolo is a shell function" }
200       { "resolved": true,  "kind": "file",     "detail": "/Users/wake/.local/bin/claude" }
200       { "resolved": false, "kind": "not-found",    "detail": "" }
200       { "resolved": false, "kind": "unverifiable", "reason": "shell_metacharacters" | "too_long" | "timeout" | "shell_failed" }
```

Everything that is not a malformed request body is a **200 with a verdict**.
There is no 504: a timeout is a verdict about the probe, not a transport
failure, and the UI renders all four outcomes the same way.

**Which shell.** The one tmux will actually start, asked of tmux:
`tmux show-options -gv default-shell` through the existing executor
(`internal/tmux/executor.go`). Falls back to `$SHELL`, then the passwd shell,
then `/bin/sh`. Invoked as an **interactive login** shell — `-l -i -c` — because
with an empty `default-command` tmux starts the pane's shell as a login shell,
and zsh sources `.zprofile`/`.zlogin` and `.zshrc` under different conditions.

**How.** The token is a positional parameter and never enters the script text:

```go
script := `builtin type -- "$1"`          // zsh, bash
// any other shell:
script  = `command -v "$1"`
exec.CommandContext(ctx, shell, "-l", "-i", "-c", script, "_", token)
```

`builtin` defeats an rc-defined `type`. A shell whose basename is neither zsh
nor bash gets the POSIX form, which still resolves functions and aliases but
yields a weaker `kind`.

**Process hygiene** — `exec.CommandContext` kills only the direct child, and an
rc file can leave descendants holding the output pipe open:

- `SysProcAttr{Setpgid: true}`, and on cancellation `syscall.Kill(-pgid, SIGKILL)`
  through `Cmd.Cancel`.
- `Cmd.WaitDelay = 1 * time.Second`, so a descendant holding the pipe cannot
  make `Wait` hang after the kill.
- stdin is `/dev/null`. stdout and stderr are read through an
  `io.LimitReader` capped at 8 KiB, so the buffer is bounded **before** the
  512-byte display truncation, not after.
- 5 s deadline.

**Rejected before exec** (`resolved: false, kind: "unverifiable"`): a token
longer than 256 bytes, or containing any of ``| & ; < > ( ) $ ` \ " ' newline``
or a leading `-`. The *template* is not restricted — only what we agree to
probe.

**The probe never blocks a save.** It reports; the user decides. And it is not
a guarantee: the probe has no tty, does not run tmux's `default-command`, and
runs on the host the user picked rather than in the pane the rebuild will
create (§9).

### 4.5 Settings UI

A new component, `spa/src/components/settings/ResumeTemplateSettings.tsx`,
rendered inside the existing **Snapshot** settings section above the rebuild
records table. A separate file, so it does not deepen issue #975
(`SnapshotSettingsSection` is already flagged for splitting); a shared section,
so the templates sit next to the records they govern.

```
Resume command templates
  Test against: [ mlab ▾ ]                         ← host picker, defaults to active host

  Claude Code
    With session id   [ cld-yolo --resume {id} ]        [Test]  ✓ shell function
    Without           [ cld-yolo -c            ]        [Test]  ✓ shell function
  Codex
    With session id   [ codex resume {id}      ]        [Test]  ✓ /opt/homebrew/bin/codex
    Without           [ codex resume --last    ]        [Test]
                                                        [Reset all to defaults]
```

- Agent rows come from `AGENT_NAMES`, so a fourth agent needs no edit here.
- Editing reuses the `EditableCwdCell` pattern (alpha.324): `committedRef`
  against double submit, `disabled` while busy, and `compositionRef` +
  `isComposing` so an IME Enter does not commit. Those were review findings on
  that component; rediscovering them here is not acceptable.
- **Inline warning, never blocking**: `exact` without `{id}` warns and still
  saves — it then resolves to the literal template, which is a legitimate if
  odd choice. `fallback` with `{id}` warns and still saves — `{id}` stays
  literal there (§4.2 step 2).
- The Test button probes that row's command word against the host in the
  picker, and renders the result until the row is edited again.
- All copy goes through i18n.

---

## 5. Design — B: the SPA asks

### 5.1 Why a request and not a broadcast

v1 of this spec pushed a second envelope from the daemon on any owner event,
throttled to once per frame. Review found two structural faults, and both are
properties of pushing rather than of the throttle:

- **No delivery guarantee.** The daemon consumed the one grant while the SPA
  had no pane bound to that session yet — the normal case, since a user opens
  Purdex before opening the tab. The write no-ops and the grant is gone. A
  full broadcast queue or a session-code lookup miss does the same.
- **No correction path.** A fill-only write cannot be corrected by a later
  fill-only write, so a mis-attribution during a frame-recovery window would
  be permanent.

Asking inverts both. The answer is a response to the asker, so it cannot be
dropped in the dark; and the SPA can ask again. It also deletes the reason the
throttle existed: the request rate is bounded by pane attaches, not by the
3805-per-session `PostToolUse` stream.

**Ownership is decided the same way it always was** — the frame-layer ancestry
walk. No new classifier, no process-ancestry rewrite, no reading ownership off
`TopFrame` (§3.2). This is the constraint the previous round settled and it is
not reopened.

### 5.2 Daemon — the frame remembers its own session id

`agent_frames` gains two columns:

```sql
ALTER TABLE agent_frames ADD COLUMN session_id TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_frames ADD COLUMN cwd        TEXT NOT NULL DEFAULT '';
```

Additive, guarded by a column-existence check next to the existing
`clearStaleSubagentsJSON` migration step. Frames are runtime state rebuilt from
live processes, so an empty column on an existing row is exactly right until
the next event fills it.

Written in `applyFrameEvent`, onto **the sender's own frame**. That needs no
ownership decision: it is that agent's session id, whoever owns the pane.
Only ever set, never cleared — a `Stop` that carries no id must not erase what
a `UserPromptSubmit` recorded.

**Reading the identity.** An optional provider interface, so the raw payload is
read once by something that knows the agent's shape:

```go
// internal/agent/provider.go
type SessionIdentifier interface {
    // IdentifyEvent extracts the sender's own session id and cwd from a raw
    // hook payload. Returns ("", "") when the event carries neither.
    IdentifyEvent(purdexName string, rawEvent json.RawMessage) (sessionID, cwd string)
}
```

Implemented by cc, codex and opencode over the shared `session_id` / `cwd` keys
(§3.1). A provider that does not implement it never contributes.

**When it runs.** Only when the stored `session_id` is empty, or the lifecycle
is `SessionStart`. So the extra JSON parse costs once per frame, not once per
event, and a `/clear` still replaces the id (cc emits `SessionStart` for it;
the `source == "compact"` early-return is untouched because a compact keeps the
same id). This is the whole of the hot-path cost of Phase 1.

**OpenCode plugin change.** `plugin_template.go` adds `cwd: pdxCwd()` to the
`PdxStop` and `PdxUserPromptSubmit` emits (§3.3). The existing `parentID`
child-session filter is untouched — v1 §9.3 states it is a precondition of the
ownership invariant for opencode.

### 5.3 Daemon — the ownership query

`GET /api/sessions/{code}/provenance`, in the agent module, alongside the
existing `/api/sessions/{code}/cwd` shape (`session/module.go:72`) — same
response convention, including the generation stamp.

```json
{ "found": true,
  "agent_type": "cc",
  "session_id": "fa657572-…",
  "cwd": "/Users/wake/Workspace/wake/purdex",
  "tmux_pane_id": "%12",
  "tmux_instance": "4465:1788754497",
  "updated_at": 1788800000000 }
```

`{ "found": false, "tmux_instance": "…" }` when there is no answer. The
generation is always present when the daemon could read it, and `""` when it
could not — the SPA treats `""` exactly as `cwd-probe.ts` does (§5.4).

Resolution, entirely over existing machinery:

1. Resolve `{code}` to the tmux session and its panes.
2. For each pane, `frames.ListByPane`, keeping only frames that are **live and
   identity-verified** — `isPidAliveFn` plus a `processStartTime` match, the
   same gating `classifyAncestor` applies.
3. A surviving frame is a **root** iff walking its own PPID chain (capped at
   `proxyMaxDepth`) finds no other live, identity-verified frame of that pane.
   This is `classifyAncestor`'s loop with the starting PID as its parameter;
   the traversal is **extracted into one shared function** so there is exactly
   one implementation, and `classifyAncestor` becomes its first caller.
4. A walk that cannot complete excludes that frame rather than promoting it:
   no evidence, no action.
5. Among roots with a non-empty `session_id`: none → `found: false`; one → that
   one; several → the most recently seen (`last_seen_at`), carrying its
   `tmux_pane_id`, which is v1 §4.4's "most recent wins" rule for a multi-pane
   tmux session, unchanged.

Read-only, no store writes, no envelope, no state between calls. The nesting
cases resolve without anything new: a nested same-type child finds the parent's
live frame above it and is not a root; a proxy-collapsed cross-type child has
no frame of its own at all. Because the query runs at attach time rather than
inside an event, there is no child-before-parent ordering to lose to.

### 5.4 SPA — the probe

`spa/src/lib/rebuild/provenance-probe.ts`, written as a **sibling of
`cwd-probe.ts` and following it rule for rule**, because every hazard that file
documents applies identically here:

- The host's attach gate (`canAttachTerminal`) must be open first.
- One request per `(hostId, sessionCode, tmuxInstance)` binding at a time
  (`inFlight`).
- A **positive** generation match is required to write: the answered
  `tmux_instance` must be non-empty and equal to the one asked with. A
  different non-empty generation marks the binding `disowned`; `''` blocks the
  write but stays retryable.
- The pane set is re-read when the request resolves, and the per-pane decision
  is made inside the store's `set`.

Two triggers, the same two, for the same reasons: the reconciled `sessions`
payload in `useMultiHostEventWs`, and pane attach in `SessionPaneContent`.

**A pane wants a provenance probe when** it is live, terminal-mode, its
generation matches, and either `rebuild.agent` is absent **or**
`rebuild.unverified` is true. The second clause is the correction path: a
record the daemon has contradicted asks again instead of staying wrong.

### 5.5 SPA — the write

New `RebuildPatch` arm, applied in `useTabStore.setPaneRebuild`:

```ts
| { kind: 'agent-backfill'; record: { tmuxInstance, agent, cwd?, resumeCommand? } }
```

- **No-op** when `prev.agent` exists and `prev.unverified` is not set. This is
  the "有了就跳過" policy, and it lives on the SPA because only the SPA knows
  what the record holds.
- Writes `agent` (type, sessionId, tmuxPaneId, updatedAt), clears `unverified`,
  sets `capturedAt`.
- **Never clears a field it does not carry.** Unlike `agent-group`, this is a
  fill: `cwd` is written only when the answer has one *and* the existing `cwd`
  is absent or `cwdSource === 'pane-probe'`. A user-edited cwd
  (`cwdSource: 'user'`) and an agent-reported one are both left alone.
- Sets `cwdSource: 'agent-backfill'` when it does write a cwd — distinct from
  `'agent-session-start'`, so the provenance of the value stays honest.
- Does **not** touch `resumeCommandOverride` (§4.3).

**Phase 1 additionally writes `resumeCommand`** through the existing
`composeResumeCommand`, exactly as the `agent-group` writer does today.
Otherwise Phase 1 would record an agent that no consumer can act on and the
pane would still rebuild as a shell. Phase 2 removes that write together with
the field.

**A user cwd edit now sets `cwdSource: 'user'`** (`useTabStore.ts:209`'s
`field` arm). Without it a hand-typed cwd keeps whatever source it inherited —
typically `'pane-probe'` — and this fill would overwrite it.

### 5.6 What this replaces

v1 §9.1's deferred `session_meta` backfill is **cancelled, not postponed**, and
that section is rewritten to say so. None of its four objections apply to a
read-only query over frames: there is no row to upsert, no `SetMeta` writer to
teach, no nil-means-no-change ambiguity, no orphan cleanup to fire.

The other §9.1 degradations stand: a pane whose agent is no longer running has
no frame, so the query answers `found: false` and the pane rebuilds as a shell;
and `unverified` still marks a record the projection disagrees with — now with
a repair path (§5.4) instead of only a warning.

---

## 6. Phases

| Phase | Content |
|---|---|
| **1** | B. Daemon: two `agent_frames` columns + migration, `SessionIdentifier` for the three agents, the shared ancestry traversal extracted from `classifyAncestor`, `GET /api/sessions/{code}/provenance`, opencode `cwd` emits. SPA: `provenance-probe.ts` + its two triggers, the `agent-backfill` patch (writing `resumeCommand` via the existing composer), `cwdSource: 'user'` on manual cwd edits. |
| **2** | A. `useResumeTemplateStore`, `resolveResumeCommand`, the `resumeCommand` → `resumeCommandOverride` rename across every site in §4.2, the identity-scoped override clear, operation-pinned display, `POST /api/shell/resolve-command`, `ResumeTemplateSettings.tsx`, i18n. |

Phase 1 first: it is the coverage bug the user has actually seen, and it is
independently useful — after Phase 1 a pre-deploy session rebuilds its agent
with the built-in command shapes.

The two phases **do** overlap in `spa/src/types/tab.ts`,
`spa/src/stores/useTabStore.ts` and `spa/src/stores/useAgentStore.ts`. Phase 2
rewrites lines Phase 1 touched; that is a rebase concern, not a design one, and
it is called out here because v1 of this spec wrongly claimed otherwise.

---

## 7. Testing strategy

**Go — identity on the frame (Phase 1).**
An event with a session id fills the column; a later event without one does not
clear it; a `SessionStart` with a new id replaces it; a `SessionStart` with
`source == "compact"` does not; the extractor is **not** invoked when the
column is already populated and the lifecycle is not `SessionStart` (asserted
through a counting stub on the provider).

**Go — the ownership query (Phase 1).** Table-driven over frame layouts, using
the `isPidAliveFn` / `processStartTimeFn` / `readProcessInfoFn` seams the
existing frame tests already use:

- one live root with an id → returned;
- root with an empty `session_id` → `found: false`;
- nested same-type (child's ancestry hits the parent's live frame) → the parent
  is returned, never the child;
- proxy-collapsed cross-type (child has no frame) → the parent is returned;
- a stale frame (PID reused, start time mismatch) does not shadow a live root;
- a walk that cannot complete excludes that frame instead of promoting it;
- two roots in different panes of one tmux session → the more recent
  `last_seen_at`, with the right `tmux_pane_id`;
- no live frames → `found: false`;
- the response's `tmux_instance` matches what `/cwd` reports for the same
  session, and is `''` when the daemon cannot read it.

The existing `provenance_test.go` assertions are **unchanged and must stay
green** — including `TestProvenance_NonSessionStart_NoEnvelope`, which remains
correct precisely because Phase 1 adds no new envelope.

**Go — probe (Phase 2).** `resolve-command`: metacharacter and oversize
rejection without exec; a malformed body → 400; timeout → 200 `unverifiable`;
exit 0 → `resolved: true`; exit 1 → `not-found`. The shell invocation sits
behind a function variable so tests substitute a stub. Two integration tests
run the real shell: one resolving a builtin, and one where the rc file spawns a
long-lived descendant holding the output pipe, asserting the request still
returns within the deadline and the process group is gone.

**Bun.** `plugin_template_bun_integration_test.go` gains an assertion that
`PdxStop` and `PdxUserPromptSubmit` emit `cwd`, alongside the existing
assertion pinning the `parentID` child filter.

**Vitest — probe client (Phase 1).** Mirrors `cwd-probe`'s existing suite:
attach gate closed → no request; in-flight dedup; a different non-empty
generation disowns the binding; `''` blocks the write but stays retryable; a
pane re-pointed mid-flight takes nothing; a pane with `unverified` asks even
though it has an agent.

**Vitest — store (Phase 1).** `agent-backfill` no-ops when `agent` exists and
`unverified` is unset; fills when it does not; replaces a `'pane-probe'` cwd;
leaves a `'user'` cwd alone; leaves an `'agent-session-start'` cwd alone;
obeys the generation guard; a later `agent-group` overwrites it wholesale; a
manual cwd edit sets `cwdSource: 'user'`.

**Vitest — resolution (Phase 2).** `resolveResumeCommand` across the three
layers × (usable id / unusable id / no id) × (override / no override); `{id}`
replaced at every occurrence; an unsafe id degrades to `fallback` and is never
interpolated; a `fallback` containing `{id}` keeps it literal; an unknown agent
yields `''`.

**Vitest — override lifecycle (Phase 2).** A `SessionStart` with the same
`agent.type` + `sessionId` keeps the override; a different id clears it; a
different type clears it.

**Vitest — UI (Phase 2).** The panel renders the composed string, not the
template; a pane with an in-flight or failed operation renders
`op.resumeCommand` even after a template changes in another window; editing
writes an override; clearing restores the template; the Test button renders
each of the four verdicts; IME composition does not commit.

**Not covered by tests, done by hand.** The reboot path (`tmux kill-server` →
rebuild all three agents, including "uncheck the resume row → expect a bare
shell"). The user runs this themselves.

---

## 8. Compatibility

Phase 1 adds an endpoint and two columns; it changes no broadcast payload, so
an SPA and a daemon at different versions behave exactly as they do today —
the older side simply never asks, or answers 404. This is a deliberate
difference from v1 of this spec, whose new envelope field an older SPA would
have read as an authoritative overwrite.

Phase 2 is SPA-only apart from the probe endpoint, which the UI treats as
optional: a 404 renders as `unverifiable`.

---

## 9. Limits (surface in UI copy)

- **Templates are global, hosts are not.** One set applies to every host; only
  the *test* is per-host. A wrapper that exists on one machine and not another
  resolves on one and fails on the other, and the Test button is how the user
  finds out.
- **The test verifies the command word, in an approximation of the pane's
  shell.** Not the arguments, not `{id}`, and not with a tty or tmux's
  `default-command`. A resolvable command can still fail at run time.
- **A pane whose agent has exited gets no answer.** The query reads live
  frames; a dead agent leaves none, and the pane rebuilds as a shell.
- **One agent group per record** (v1 §4.4) — unchanged. A tmux session running
  two root agents in two panes records the more recent.

---

## 10. Review disposition — codex `task-mtre55dl-hnlmvd` on v1

| # | Finding | Disposition |
|---|---|---|
| 1 | Blocker — child-first mis-attribution could not self-correct under a fill-only push | **Accepted.** Structural: a query runs after ancestry has settled, and `unverified` re-asks (§5.3, §5.4) |
| 2 | Blocker — daemon "granted" ≠ SPA "written"; the grant could be consumed with no pane bound | **Accepted.** Structural: the answer is a response to the asker (§5.1) |
| 3 | Important — a new daemon's backfill envelope would be read as an overwrite by an alpha.332 SPA | **Moot.** No new envelope (§8) |
| 4 | Important — Phase 1 alone produced an agent no consumer could act on; the "no shared files" claim was false | **Accepted.** Phase 1 writes `resumeCommand` via the existing composer; the overlap is stated (§5.5, §6) |
| 5 | Important — arming map: unstable reset rate, marked only on success, unspecified lock | **Moot.** No arming map (§5.1) |
| 6 | Important — `exec.CommandContext` kills only the direct child; unbounded capture | **Accepted** (§4.4 process hygiene) |
| 7 | Important — `-i` is not login; `$SHELL` is not tmux's `default-shell`; an rc-defined `type` | **Accepted**: ask tmux, `-l -i`, `builtin type` (§4.4) |
| 8 | Important — "the override is necessarily stale" does not hold | **Accepted.** Cleared only on an agent identity change (§4.3) |
| 9 | Important — a user-edited cwd is indistinguishable from a probed one | **Accepted.** `cwdSource: 'user'` (§4.1, §5.5) |
| 10 | Important — a retry would send a command the panel no longer shows | **Accepted.** An operation pins the displayed string (§4.3) |
| 11 | Important — the rename list was incomplete | **Accepted.** Full list, and `useRebuildStore`'s field is explicitly excluded (§4.2). Their finding that a stale JSON key is inert is adopted as the justification for no migration (§4.1) |
| 12 | Minor — contradictory HTTP contract, a false "all existing tests pass" claim, undefined template-violation behaviour | **Accepted** (§4.2, §4.4, §4.5, §7) |

---

## 11. Deferred

- **Per-host template overrides** — only if the global set proves wrong.
- **Launch-flag reconstruction from argv** — v1 §9.2, unchanged.
- **Unifying the two identity readers** — `result.Detail` for the `SessionStart`
  envelope, `SessionIdentifier` for the frame column. Two readers of one fact,
  kept apart so Phase 1 does not perturb shipped `SessionStart` behaviour.
