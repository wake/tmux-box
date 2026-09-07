# Spec — Resume command templates & provenance backfill

**Status:** v5 — approved for plan by codex round 4
**Review history:** v1 → codex `task-mtre55dl-hnlmvd` (2 Blocker, 9 Important, 1 Minor); v2 → codex `task-mtrggpct-ub3biv` (2 Blocker, 8 Important, 2 Minor); v3 → codex `task-mtrgvcef-75s6gi` (1 Blocker, 4 Important, 2 Minor); v4 → codex `task-mtrh5xai-ipphk9` (**0 Blocker, approved**). Dispositions in §10.
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
- Running the template to verify it. Only the command word is resolved (§4.4).
- A new notion of ownership. §5 reuses the existing frame-layer ancestry walk
  and the existing pane-tree verification, and adds nothing else.

---

## 3. Evidence (measured on this machine, 2026-09-07/08)

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

### 3.2 What the frame layer already verifies, and what it does not store

`agent_frames` (`internal/store/frames.go:34`) holds one row per agent process
per tmux pane, with `pid`, `ppid`, `process_start_time`, `parent_frame_id` and
`subagents_json`.

Two existing checks matter here, and **both** are needed by §5.3:

- `classifyAncestor` (`ancestor.go:47`) walks a PID's ancestry against those
  rows and reports `VerdictRoot` when no live, identity-verified frame sits
  above it in the same pane.
- `verifyEvent` (`verify.go:40`) additionally proves the process is a
  **descendant of the pane's current process** — `resolvePanePIDFn` +
  `pidAncestorIncludesFn` (`verify.go:60-65`) — and that the provider still
  identifies it. Alive-plus-start-time alone is *not* the codebase's bar for
  "this process belongs to this pane", and a query that skipped it could hand
  back a surviving old agent under a reused pane id.

What the rows do **not** hold is the agent's own session id — that has only
ever existed in flight, inside a hook payload. §5.2 adds it.

`SessionProjection.TopFrame` is **not** the owner: `buildPaneProjection`
(`projection.go:111-119`) sorts by `started_at` and takes the **last**, i.e.
the innermost / most recent agent, which is what the lights UI wants. The
ancestry walk is the only ownership answer in this codebase and this spec adds
no second one.

### 3.3 OpenCode's non-`SessionStart` emits omit `cwd`

`internal/agent/opencode/plugin_template.go` calls `pdxCwd()` only for
`PdxSessionStart` (line 108). `PdxStop` (145) and `PdxUserPromptSubmit` (177)
send `session_id` alone. Adding `cwd: pdxCwd()` to those two emits is a
one-line change each.

OpenCode also switches back to an **existing** session inside one process. Only
`session.created` emits `PdxSessionStart`, while the following `chat.message`
carries the now-current `input.sessionID` (`plugin_template.go:97,137,169`). So
the stored session id must be updated by ordinary events, not only by
`SessionStart` (§5.2).

### 3.4 A process read on darwin costs four `ps` forks

`readProcessInfoPlatform` (`internal/agent/process_info_darwin.go:9`) shells out
four times per call — ppid, `comm`, `args`, start time. An ancestry walk capped
at `proxyMaxDepth` therefore costs up to ~20 forks **per frame**, and §5.3
walks every frame in the pane. Memoization within one request is a requirement,
not an optimisation (§5.3).

### 3.5 An agent sits one process below its tmux pane

Measured on this machine, 2026-09-08, over `tmux list-panes -a` and
`ps -axo pid,ppid,comm`. For every pane currently running an agent:

```
pane pid 46435 (-zsh)
  depth=1  47858  /Users/wake/.local/bin/claude
  depth=2  47967  node                     ← MCP servers, not the agent
  depth=2  48486  /bin/zsh
```

**Depth 1.** A shell function adds no process layer — `cld-yolo` execs `claude`
from the pane's own shell — so the wrapper this feature exists for does not
deepen the chain. An npm launcher (codex installed through node) would make it
2. `proxyMaxDepth` is 5, so the pane-tree half of §5.3's walk has ample
headroom, and the walk's existing behaviour of refusing rather than inferring
past the cap is the right failure mode for anything deeper.

### 3.6 A shell function is invisible to a non-interactive shell

`cld-yolo` is defined in the user's `~/.zshrc`. It is not on `PATH` and
`command -v cld-yolo` from a non-interactive shell finds nothing. It resolves
only in a shell that has sourced the rc file — which is what the rebuild engine
gets, because it delivers the command with tmux `send-keys` into the shell tmux
started. Any save-time verification must therefore reproduce that shell's
startup, or it will report a false negative on the very case this feature
exists for.

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
+   * Never written automatically; cleared when the agent identity it was
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
`fallback`, never to an interpolated command. `{id}` is the only placeholder
and there is no escape for a literal brace.

**Every consumer of the old field becomes a call to this function.** The full
list, so the rename cannot be done half-way:

| File | Sites |
|---|---|
| `spa/src/types/tab.ts` | the field; the `RebuildPatch` `field` union |
| `spa/src/stores/useAgentStore.ts` | 94 — stops storing a command |
| `spa/src/stores/useTabStore.ts` | 200 (agent-group write), 209 (`field` patch) |
| `spa/src/components/RebuildActionSet.tsx` | 18 (`RebuildEditableField`), 226, 229, 294, 298, 379 |
| `spa/src/components/RenamePopover.tsx` | 149, 153 |
| `spa/src/components/TerminatedPane.tsx` | wherever it forwards the field |
| `spa/src/lib/rebuild/batch.ts` | 68, 82 |
| `spa/src/lib/rebuild/eligibility.ts` | any read of the field |
| `spa/src/lib/rebuild/engine.ts` | 164 (`publishRefusal`), 496-502 |
| `spa/src/components/settings/SnapshotSettingsSection.tsx` | 664 |
| fixtures | `useTabStore.rebuild.test.ts`, `RebuildActionSet.test.tsx`, `RenamePopover.rebuild.test.tsx`, `composer.test.ts`, `batch.test.ts`, `engine.test.ts`, `eligibility.test.ts` |

**`useRebuildStore.ts:40`'s `resumeCommand` keeps its name.** It is not the
record field — it is the string an in-flight operation pinned, and renaming it
along with the record field would blur exactly the distinction §4.3 depends on.

### 4.3 Override lifetime, and what the panel shows

**Clearing.** `resumeCommandOverride` is cleared by any writer that changes the
**agent identity** the record holds — a different `agent.type` or a different
`agent.sessionId`. That covers a qualifying `SessionStart` with a new id and
the identity-correcting backfill of §5.5. A writer that lands the same identity
(an idle `SessionStart` re-emit, or the **confirm** mode of §5.5) leaves the
override alone.

This is a **product policy, not a proof**. It is neither necessary nor
sufficient in every case: an override of the form `cld-yolo -c` stays valid
across an identity change and is nevertheless discarded, while an override can
go stale for reasons the identity does not capture (the recorded `cwd` moved
under it). It is chosen because the one hazard that silently does the *wrong
thing* — a verbatim command carrying a dead session id — is exactly an identity
change, and because a discarded override is visible and retypable while a
silently stale one is not. v1's unconditional clear on every `SessionStart` is
narrowed to this.

**Display.** The panel renders `resolveResumeCommand(...)` **except** while the
pane's rebuild operation is in flight, or has already created a session
(`op.created` present) — in those two states, and only those, every row renders
`op.resumeCommand`, the string the engine pinned at operation start. This is
what keeps "Retry resume" honest: retry acts on the created session and must
show the command it will actually re-send. An operation that failed *before*
creating anything is not actionable — the next Rebuild recomputes from scratch —
so the panel goes back to the live resolution, and a template edited meanwhile
is reflected before the user presses the button.

### 4.4 Save-time verification

**What is verified.** The **command word only** — the first whitespace-
separated token of the template, with `{id}` never substituted.

**Endpoint.** `POST /api/shell/resolve-command`, in the session module
(`internal/module/session/`), which already owns shell and tmux execution.

```
request   { "command": "cld-yolo" }
400       malformed body — `command` missing or not a string
200       { "resolved": true,  "detail": "/Users/wake/.local/bin/claude" }
200       { "resolved": true,  "detail": "cld-yolo" }
200       { "resolved": true,  "detail": "alias cld='cld-yolo'" }
200       { "resolved": false, "reason": "not_found" }
200       { "resolved": false, "reason": "shell_metacharacters" | "too_long" | "timeout" | "shell_failed" }
```

Everything that is not a malformed request body is a **200 with a verdict**.
There is no 504: a timeout is a verdict about the probe, not a transport
failure, and the UI renders all outcomes the same way.

**There is no `kind` field, deliberately.** `resolved` comes from the exit
status and `detail` is what the shell printed; the API makes **no claim about
what species of thing was found**. Two earlier drafts tried and both were
wrong. Parsing `type` output does not survive the shell difference — zsh prints
`demo_fn is a shell function from zsh`, bash prints `demo_fn is a function`
*followed by the entire function body*. Classifying `command -v` output by
shape does not survive PATH: measured on this machine, an alias prints its whole
definition (`alias demo_alias='/bin/echo hello'`), and a PATH entry that is
relative prints a relative path (`usr/bin/dirname`), so "starts with `/`" is not
"is a file" and "one word" is not "is a shell word". The user does not need the
species — they need to know it resolves, and to what. `detail` is the
**last non-empty line** of stdout, so rc chatter before the answer is dropped;
a multi-line alias definition is therefore shown truncated, which is a display
limitation and not a wrong verdict.

**Which shell.** The one tmux will actually start, asked of tmux:
`show-options -gv default-shell` through the existing executor
(`internal/tmux/executor.go`, which needs a new server/global-option method —
`ShowWindowOption` passes `-w` and is not usable here). If the tmux server is
not running the lookup fails, and the probe falls back to `$SHELL`, then the
passwd shell, then `/bin/sh`. Invoked as an **interactive login** shell —
`-l -i -c` — because with an empty `default-command` tmux starts the pane's
shell as a login shell.

**How.** The token is a positional parameter and never enters the script text:

```go
script := `builtin command -v "$1"`   // zsh, bash
// any other shell:
script  = `command -v "$1"`
exec.CommandContext(ctx, shell, "-l", "-i", "-c", script, "_", token)
```

`builtin` defeats an rc-defined `command`.

**Process hygiene** — `exec.CommandContext` kills only the direct child, and an
rc file can leave descendants holding the output pipe open:

- `SysProcAttr{Setpgid: true}`, and on cancellation `syscall.Kill(-pgid, SIGKILL)`
  through `Cmd.Cancel`.
- `Cmd.WaitDelay = 1 * time.Second`, so a descendant holding the pipe cannot
  make `Wait` hang after the kill.
- stdin is `/dev/null`; stdout and stderr are read through an `io.LimitReader`
  capped at 8 KiB, so the buffer is bounded **before** the 512-byte display
  truncation.
- 5 s deadline.

**Rejected before exec** (`resolved: false`, with `reason` `"shell_metacharacters"`
or `"too_long"`): a token
longer than 256 bytes, or containing any of ``| & ; < > ( ) $ ` \ " ' newline``
or a leading `-`. The *template* is not restricted — only what we agree to
probe.

**The probe never blocks a save**, and it is an approximation, not a
guarantee — see §9.

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
    With session id   [ cld-yolo --resume {id} ]        [Test]  ✓ cld-yolo
    Without           [ cld-yolo -c            ]        [Test]  ✓ cld-yolo
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
  saves — it then resolves to the literal template. `fallback` with `{id}` warns
  and still saves; `{id}` stays literal there (§4.2 step 2).
- **A test result is keyed by `(hostId, commandWord)`** and is shown only while
  both still match. Switching the host picker, or editing the row, clears it;
  a response that arrives after either changed is discarded. A verdict from
  another machine must never sit next to a command the user is now judging for
  this one.
- All copy goes through i18n.

---

## 5. Design — B: the SPA asks

### 5.1 Why a request and not a broadcast

v1 of this spec pushed a second envelope from the daemon on any owner event,
throttled to once per frame. Review found two structural faults, and both are
properties of pushing rather than of the throttle:

- **No delivery guarantee.** The daemon consumed the one grant while the SPA
  had no pane bound to that session yet — the normal case, since a user opens
  Purdex before opening the tab. A full broadcast queue or a session-code
  lookup miss does the same.
- **No correction path.** A fill-only write cannot be corrected by a later
  fill-only write.

Asking inverts both, and deletes the reason the throttle existed: the request
rate is bounded by SPA triggers, not by the 3805-per-session `PostToolUse`
stream. Ownership is still decided by the frame-layer ancestry walk plus the
pane-tree check that every accepted event already passes (§3.2). No new
classifier, no reading ownership off `TopFrame`.

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

**These two columns never travel inside a `Frame` round-trip write.** That is
the whole concurrency design, and it is what keeps them out of the optimistic
retry loops:

Every method of `FramesStore`, so nothing is left to inference:

| Store method | session_id / cwd |
|---|---|
| `Upsert` (`frames.go:124`) — the `INSERT` column list | written from the struct |
| `Upsert` — the `ON CONFLICT … DO UPDATE SET` list (`frames.go:165-173`) | **omitted**, so an existing row keeps its stored identity. The method then re-`SELECT`s through `GetByIdentity` (`frames.go:179`) and returns the persisted row, so the returned struct carries the stored value **by construction** — no zero-value merge is added, and none is needed |
| `UpsertIfUnchanged` (`frames.go:372`) | **omitted from the SQL** — the proxy-attach retry loop reloads and re-writes whole rows, and including them would let a reloaded stale baseline clobber a fresh id |
| `UpdateHookPath` (`frames.go:305`) / `UpdateHookPathAndResetSubagents` (`frames.go:337`) | **omitted** — this is the narrow UPDATE that ordinary hook events take |
| `UpdateStatusAndLastSeen` (`frames.go:280`) | **omitted** |
| `Delete` (`frames.go:254`) / `DeleteIfUnchanged` (`frames.go:263`) | whole-row delete; the identity goes with the frame, which is correct — the process is gone |
| **`UpdateSessionIdentity(frameID, sessionID, cwd)`** (new) | the **only** post-insert writer: a two-column UPDATE keyed by `frame_id`, each column written only when the incoming value is non-empty |
| `GetByIdentity`, `FindByPanePID`, `ListByPane`, `ListAll`, and the shared `scanFrame` | included, so every read is complete |

Because identity is written by its own statement, no CAS retry has to
re-apply it and no read-modify-write can lose it.

**Where it is called.** In `applyFrameEvent`, after the frame mutation, for the
**sender's own frame**. That needs no ownership decision: it is that agent's
session id, whoever owns the pane. Ordinary hook events are the important
caller — they are how a pre-deploy session's frame acquires an id at all, and
`UpdateHookPath` is the path they take.

**No lifecycle gate.** v2 proposed parsing only when the column was empty or
the lifecycle was `SessionStart`. That is wrong: opencode switches back to an
existing session without a `SessionStart` (§3.3), so the gate would freeze the
record on the old conversation, and it would also stop a frame that learnt its
id from a `cwd`-less event from ever acquiring a `cwd`. Every event from the
sender's own frame contributes; only a **non-empty** extracted value is
written, so a payload that carries neither clears nothing.

**Reading the identity cheaply.** cc `PostToolUse` payloads embed whole tool
inputs, so a full `map[string]any` unmarshal per event is not acceptable. The
extractor decodes into a **typed struct with exactly the two fields**:

```go
var id struct {
    SessionID string `json:"session_id"`
    Cwd       string `json:"cwd"`
}
```

`encoding/json` skips an unknown field's value without materialising it, so a
large `tool_input` costs a scan and no allocation. A `map[string]json.RawMessage`
would *not* have been enough — v3 claimed it "parses only the top-level key
structure", which is wrong: `RawMessage.UnmarshalJSON` **copies** every value's
bytes, so the big field would have been allocated anyway. Plan adds a
`benchmem` benchmark over a representative large payload to hold this.

```go
// internal/agent/provider.go
type SessionIdentifier interface {
    // IdentifyEvent extracts the sender's own session id and cwd from a raw
    // hook payload. Returns ("", "") when the event carries neither.
    IdentifyEvent(purdexName string, rawEvent json.RawMessage) (sessionID, cwd string)
}
```

Implemented by cc, codex and opencode over the shared keys (§3.1). A provider
that does not implement it never contributes.

**OpenCode plugin change.** `plugin_template.go` adds `cwd: pdxCwd()` to the
`PdxStop` and `PdxUserPromptSubmit` emits (§3.3). The existing `parentID`
child-session filter is untouched — v1 §9.3 states it is a precondition of the
ownership invariant for opencode.

### 5.3 Daemon — the ownership query

`GET /api/sessions/{code}/provenance`, mirroring `/api/sessions/{code}/cwd`
(`session/module.go:72`) including its two-sided generation sampling.

```json
{ "found": true,
  "agent_type": "cc",
  "session_id": "fa657572-…",
  "cwd": "/Users/wake/Workspace/wake/purdex",
  "tmux_pane_id": "%12",
  "tmux_instance": "4465:1788754497",
  "last_seen_at": 1788800000000 }
```

`{ "found": false, "tmux_instance": "…" }` when there is no answer.
`tmux_instance` is sampled **before and after** the frame work, exactly as the
cwd handler does, and reported as `""` if the two samples disagree or the read
fails — `""` authorises nothing on the SPA side (§5.4).

Resolution:

1. Resolve `{code}` to the tmux session and its panes.
2. Resolve each pane's **current** process (`resolvePanePIDFn`, whose real
   signature is `func(tmux.Executor, string) (int, error)`). A pane whose PID
   cannot be resolved contributes nothing.
3. For each frame from `frames.ListByPane`, keep it only if it is alive and its
   `processStartTime` matches the stored value.
4. **One walk per surviving frame answers both questions.** Walking
   `frame.PID`'s PPID chain, the traversal reports:
   - whether `panePID` appears on the chain — the pane-tree check every
     accepted event passes (`verify.go:60-65`), and what stops a surviving
     agent from a previous tmux generation being handed back under a reused
     pane id;
   - whether any *other* surviving frame of that pane appears on it — which
     makes this frame a non-root.

   A frame is a **root** iff the first is true and the second is false.

   The traversal is `classifyAncestor`'s loop **extracted into one shared
   function**, with `classifyAncestor` as its first caller, so the depth cap,
   the self-parent guard and the unreadable-process rule have one
   implementation. The walk needs only a boolean here — "something framed is
   above me" — so `VerdictSameTypeAbove`'s hard stop is correct for this use;
   the ancestor it returns is *not* claimed to be the outermost one.

   **`pidAncestorIncludesFn` is not called.** v3 proposed reusing it and that
   was wrong on two counts: `PidAncestorIncludes` (`probe/liveness.go:303`)
   walks with **no depth cap**, and it calls `agentpkg.ReadProcessInfo`
   directly rather than the `readProcessInfoFn` seam — so it would bypass both
   the memo and the test stubs, and the cost contract below could not be met or
   asserted. Its *semantics* are what step 4 reproduces, including that a frame
   whose PID equals the pane PID counts as inside the tree.

   The projection's own pane filter (`frame_ops.go:951-978`) is **also** not
   reused: it keeps a frame when resolution fails, which is the opposite of the
   policy here.
5. A walk that cannot complete excludes that frame rather than promoting it:
   no evidence, no action.
6. Among roots with a non-empty `session_id`: none → `found: false`; one → that
   one; several → the largest `last_seen_at`, ties broken by `frame_id`, and
   the answer carries its `tmux_pane_id`.

**The multi-root tie-break is a new rule, not the old one.** v1 §4.4 chose the
most recent qualifying `SessionStart`; `last_seen_at` is advanced by any hook
or status update, so with two root agents in two panes of one tmux session the
two writers can disagree (A started first but acted last: the `SessionStart`
writer picks B, this query picks A). Both answers name a real root agent of the
session; this spec accepts the divergence rather than adding a column to
paper over it, and §9 states it.

**Cost.** §3.4: one process read is four `ps` forks on darwin, and this walks
every frame in the pane. So the handler builds **one memoizing process reader
per request**, wrapping `readProcessInfoFn`, and every walk in the request goes
through it — including the pane-tree half of step 4, which is why that half had
to stop being a separate helper. Each PID is then read at most once for the
whole request. Frames in a pane share almost all of their ancestry, so the memo
turns O(frames × depth) reads into roughly O(distinct PIDs).

The request carries a 5 s deadline, **checked between process reads**. It
cannot interrupt one: `readProcessInfoPlatform` uses `exec.Command(...).Output()`
with no context (`process_info_darwin.go:9`), and giving it one is a change to
a shipped hot path that this spec does not make (§11). A single `ps` does not
hang for long, and the deadline still bounds the walk.

The memo is per-request, never shared, so it cannot serve stale ancestry to a
later call. Read-only: no store writes, no envelope, no state between calls.

### 5.4 SPA — the probe

`spa/src/lib/rebuild/provenance-probe.ts`, a sibling of `cwd-probe.ts` reusing
its named helpers rather than paraphrasing its rules:

- The host's attach gate (`canAttachTerminal`) must be open first; empty
  `hostId` / `sessionCode` return immediately.
- One request per `(hostId, sessionCode, tmuxInstance)` binding at a time
  (`inFlight`), cleared in `finally`.
- **Pane eligibility** uses `generationMatchesLegacy` (`binding.ts:46`), which
  is deliberately one-way: a pane whose recorded instance is `''` matches a
  known expected generation.
- **Authorising the write** is the stricter, separate test the cwd probe
  applies: the answered generation must be non-empty **and** equal to the one
  asked with. These two are not the same test and the implementation must not
  merge them.
- **`disowned`** is recorded only when the requested and answered generations
  are **both non-empty and different** — proof the code was reused. An answered
  `''`, or a requested `''`, blocks the write but stays retryable.
- The pane set is re-read when the request resolves, and the per-pane decision
  is made inside the store's `set`.

**A pane wants a provenance probe when** it is live, terminal-mode, its
generation matches, and either `rebuild.agent` is absent **or**
`rebuild.unverified` is true. Nothing else makes a pane eligible: a record with
a confirmed agent never asks again, which is what makes the whole thing
terminate (§5.5).

**Three triggers**, because two are not enough:

1. The reconciled `sessions` payload in `useMultiHostEventWs` — sweeps every
   pane on the host, but only fires when the session list changes.
2. Pane attach in `SessionPaneContent` — covers a pane opened after the list
   settled.
3. **A hook broadcast for a session whose pane still wants provenance.** This
   is the trigger v2 lacked, and without it the everyday case fails: the first
   probe runs before any event has filled the frame's `session_id`, gets
   `found: false`, and nothing ever asks again — the session list has not
   changed and the pane is not re-attached. The hook stream is exactly the
   signal that the daemon now knows more than it did.

#### 5.4.1 The re-query state machine

Rate limiting a trigger by dropping requests loses the one hook that mattered:
a session that emits a single event at t=5 s and then goes idle would be
skipped by a bare cooldown and never asked again. So a suppressed trigger is
**deferred, never dropped**. Per binding:

```
{ nextAllowedAt: number, pending: boolean, timer: handle | null }
```

- A trigger fires a request immediately when nothing is in flight and
  `now >= nextAllowedAt`.
- Otherwise it sets `pending = true` and, if no timer is armed, arms one for
  `nextAllowedAt`. **Later triggers never move `nextAllowedAt`** — it is
  computed only when a request *completes*, as `completedAt + 30 s`. A busy
  session therefore cannot starve its own deferred run by pushing the deadline
  forward, which is the failure a debounce would have.
- When the timer fires, or an in-flight request completes with `pending` set,
  exactly **one** further request runs and `pending` clears. Coalescing is the
  point: ten hooks during a cooldown buy one re-query, not ten.
- Before that deferred run, the binding, the attach gate and the pane
  eligibility are **re-checked**. A pane that stopped wanting an answer in the
  meantime issues no request.
- Cooldown state lives beside `inFlight` / `disowned` in the module and is
  cleared by the same `reset*` test seam.

**This terminates.** The only states that keep a pane eligible are "no agent"
and "unverified", and §5.5 guarantees every answer leaves at least one of them
closed: an answer with an identity ends "no agent"; an answer that agrees with
an `unverified` record clears the flag; an answer that disagrees replaces the
record. A binding that keeps answering `found: false` costs one request per 30 s
**only while hooks keep arriving**, and none at all once the session is idle.

`unverified` cannot be re-raised in a loop, because the only writer is the
**reconnect replay branch** (`useAgentStore.ts:213`) — ordinary hook broadcasts
never set it. So after a `confirm`, no amount of further agent activity
re-opens the question; a *new* reconnect can raise it again, and that is a new
signal deserving one more repair, not a loop. "Never asks again" throughout
this section means "until a new invalidation signal arrives", and there is
exactly one such signal.

`disowned` remains permanent, as in `cwd-probe`.

### 5.5 SPA — the write

New `RebuildPatch` arm, applied in `useTabStore.setPaneRebuild`:

```ts
| { kind: 'agent-backfill'; record: { tmuxInstance, agent, cwd?, resumeCommand? } }
```

The generation guard is unchanged: the write matches panes on
`(hostId, sessionCode, tmuxInstance)`.

**Four modes, evaluated in order; the first match wins.** They are mutually
exclusive by construction, which v3's table was not — it let "agent present,
same identity, verified" match two rows at once:

| # | Condition | Mode | Effect |
|---|---|---|---|
| 1 | `prev.agent` absent | **fill** | writes `agent`; writes `cwd` only if the answer has one and the existing `cwd` is absent or `cwdSource === 'pane-probe'`; sets `cwdSource: 'agent-backfill'` when it writes one; leaves `resumeCommandOverride` alone |
| 2 | `prev.unverified` **and** the answer's `type` or `sessionId` differs | **replace** | writes the whole agent group as one unit, exactly as `agent-group` does: new `agent`, the answer's `cwd` (or none), `unverified` cleared, **and `resumeCommandOverride` cleared** (§4.3). A `cwdSource: 'user'` cwd is the one thing kept |
| 3 | `prev.unverified` **and** the answer's identity matches | **confirm** | clears `unverified` and nothing else |
| 4 | otherwise (`agent` present and verified) | **no-op** | the "有了就跳過" policy |

**Mode 3 is the convergence rule.** Without it an `unverified` record whose
agent the daemon agrees with would stay flagged and stay eligible forever,
re-asking every 30 s for the life of the session — the projection's `TopFrame`
type can legitimately differ from the ancestry root indefinitely (§3.2), so the
flag would never lift on its own. An ancestry answer that names the same agent
is positive evidence the record is right, and saying so is what ends the loop.

**Mode 2** is why the fill-only rule of v2 was not enough: correcting the agent
while leaving the previous agent's `cwd` and override attached would recreate
exactly the cross-identity mixture v1 §4.1 introduced whole-group writes to
prevent.

**There is no "refresh" mode, and no promise to fill a late `cwd`.** v3 had
one, and it was unreachable: once a fill succeeds the record has an agent and
is not `unverified`, so the pane stops being eligible and the refresh could
never run. Rather than widen eligibility to chase it — which would make every
verified pane ask forever — the promise is withdrawn. It costs little: the cwd
probe already supplies a `cwd` independently, so what is lost is a provenance
upgrade from `'pane-probe'` to `'agent-backfill'`, not the directory itself.
The same withdrawal applies to opencode's in-process session switch (§3.3):
the daemon's column follows it, but a record that is already verified will not
ask again, and `unverified` is only raised by the replay's agent-**type**
comparison. §9 states both.

**Phase 1 additionally writes `resumeCommand`** through the existing
`composeResumeCommand` — but **only when the record's `resumeCommand` is
empty**, so a command the user typed by hand into an agent-less record is not
overwritten by a composed default. (In **replace** mode the composed command
replaces the old one along with the rest of the group.) Phase 2 removes this
write together with the field.

**A user cwd edit now sets `cwdSource: 'user'`** (`useTabStore.ts:209`'s `field`
arm). Without it a hand-typed cwd keeps whatever source it inherited —
typically `'pane-probe'` — and **fill** would overwrite it.

### 5.6 What this replaces

v1 §9.1's deferred `session_meta` backfill is **cancelled, not postponed**, and
that section is rewritten to say so. None of its four objections apply to a
read-only query over frames: there is no row to upsert, no `SetMeta` writer to
teach, no nil-means-no-change ambiguity, no orphan cleanup to fire.

`unverified` keeps its existing meaning and gains a repair path (§5.4, §5.5)
instead of only a warning.

---

## 6. Phases

| Phase | Content |
|---|---|
| **1** | B. Daemon: two `agent_frames` columns + migration + the write contract of §5.2, `UpdateSessionIdentity`, `SessionIdentifier` for the three agents, the shared ancestry traversal extracted from `classifyAncestor`, `GET /api/sessions/{code}/provenance` with pane-tree verification and per-request memoization, opencode `cwd` emits. SPA: `provenance-probe.ts` + its three triggers and stop conditions, the `agent-backfill` patch with its four ordered modes, `cwdSource: 'user'` on manual cwd edits. |
| **2** | A. `useResumeTemplateStore`, `resolveResumeCommand`, the `resumeCommand` → `resumeCommandOverride` rename across every site in §4.2, the identity-scoped override clear, operation-pinned display, `POST /api/shell/resolve-command`, `ResumeTemplateSettings.tsx`, i18n. |

Phase 1 first: it is the coverage bug the user has actually seen, and after it a
pre-deploy session rebuilds its agent with the built-in command shapes.

The two phases **do** overlap in `spa/src/types/tab.ts`,
`spa/src/stores/useTabStore.ts` and `spa/src/stores/useAgentStore.ts`. Phase 2
rewrites lines Phase 1 touched; that is a rebase concern, not a design one, and
it is called out because v1 of this spec wrongly claimed otherwise.

---

## 7. Testing strategy

**Go — identity on the frame (Phase 1).** An ordinary hook event on an existing
frame (the `UpdateHookPath` path) writes the id; an event carrying no id does
not clear it; a **different** id from an ordinary event replaces it (the
opencode session-switch case, §3.3); a `cwd` arriving later than the id fills
in; a `SessionStart` with `source == "compact"` changes nothing. Concurrency: a
proxy attach retry loop interleaved with an identity write leaves both the
merged subagents list and the new id intact — pinning the §5.2 table. A
`benchmem` benchmark over a cc `PostToolUse` payload with a large `tool_input`
holds the extractor's allocation claim (§5.2).

**Go — the ownership query (Phase 1).** Table-driven over frame layouts, using
the `isPidAliveFn` / `processStartTimeFn` / `readProcessInfoFn` /
`resolvePanePIDFn` / `pidAncestorIncludesFn` seams the existing frame tests use:

- one live root with an id → returned;
- root with an empty `session_id` → `found: false`;
- **pane id reused, old agent process still alive, old row not yet swept** →
  `found: false`, because the pane-tree check fails. This is the Blocker case
  from review round 2 and gets its own test;
- nested same-type → the parent, never the child;
- proxy-collapsed cross-type (child has no frame) → the parent;
- a stale frame (PID reused, start-time mismatch) does not shadow a live root;
- a walk that cannot complete excludes that frame instead of promoting it;
- two roots in two panes of one tmux session → the larger `last_seen_at`, with
  the right `tmux_pane_id`; equal `last_seen_at` → the `frame_id` tie-break;
- generation sampling: two disagreeing samples → `tmux_instance: ""`;
- **process reads are memoized**: a layout with a shared ancestor chain asserts
  `readProcessInfoFn` is called once per distinct PID for the whole request.

The existing `provenance_test.go` assertions are unchanged and must stay green,
including `TestProvenance_NonSessionStart_NoEnvelope` — correct precisely
because Phase 1 adds no new envelope.

**Go — probe (Phase 2).** `resolve-command`: metacharacter and oversize
rejection without exec; malformed body → 400; timeout → 200 with
`reason: "timeout"`; exit 1 → `reason: "not_found"`; exit 0 returns the last
non-empty line as `detail`, so rc chatter before the answer is ignored. Real
shells are exercised over the shapes measured in §4.4 — absolute path, relative
PATH entry, function, alias, builtin, keyword — each asserting `resolved` only,
because the API makes no claim beyond it. The shell invocation sits
behind a function variable so tests substitute a stub. Two integration tests run
the real shell: one resolving a builtin, and one where the rc file spawns a
long-lived descendant holding the output pipe, asserting the request returns
within the deadline and the process group is gone.

**Bun.** `plugin_template_bun_integration_test.go` gains an assertion that
`PdxStop` and `PdxUserPromptSubmit` emit `cwd`, alongside the existing
assertion pinning the `parentID` child filter.

**Vitest — probe client (Phase 1).** Attach gate closed → no request;
in-flight dedup; both-non-empty-and-different disowns, and neither an answered
`''` nor a requested `''` does; a pane re-pointed mid-flight takes nothing; a
pane with `unverified` asks even though it has an agent.

The §5.4.1 state machine gets its own suite, on fake timers, because it is the
part review has broken twice:

- **the single-hook case**: `found: false` at t=0, one hook broadcast at t=5 s
  while the cooldown holds, then total silence — a request must still run when
  the cooldown expires. This is the round-3 Blocker and it fails against a
  drop-on-cooldown implementation;
- **no deadline extension**: hooks at t=5, 10, 15, 20 s produce exactly one
  deferred request, and it runs at t≈30 s, not at t≈50;
- **coalescing**: ten hooks inside one cooldown buy one request;
- **in-flight**: a hook arriving while a request is open schedules exactly one
  follow-up after it completes;
- **re-checked before the deferred run**: a pane that gained an agent, was
  re-pointed, terminated, or lost its attach gate during the cooldown issues
  no request;
- **termination**: after a `confirm` the pane makes no further request on any
  number of broadcasts.

**Vitest — store (Phase 1).** The four modes of §5.5 as an ordered table,
including the case v3 left ambiguous (agent present, same identity, verified →
**no-op**, matched by row 4 and not by any earlier row). Plus: `confirm` clears
`unverified` and changes nothing else; a `'user'` cwd survives fill and
replace; a `'pane-probe'` cwd is replaced; the generation guard; a later
`agent-group` overwrites; a manual cwd edit sets `cwdSource: 'user'`; **a
hand-typed `resumeCommand` on an agent-less record survives a fill**.

**Vitest — resolution (Phase 2).** Three layers × (usable id / unusable id / no
id) × (override / no override); `{id}` replaced at every occurrence; an unsafe
id degrades to `fallback` and is never interpolated; a `fallback` containing
`{id}` keeps it literal; an unknown agent yields `''`.

**Vitest — override lifecycle (Phase 2).** Same identity keeps the override; a
different sessionId clears it; a different type clears it; the replace-mode
backfill clears it.

**Vitest — UI (Phase 2).** The panel renders the composed string, not the
template; an in-flight operation and one with `op.created` render
`op.resumeCommand` even after a template changes in another window; **an
operation that failed before creating anything goes back to the live
resolution, and the next Rebuild sends what the panel shows**; editing writes
an override; clearing restores the template; the Test button renders each
verdict; a result is dropped when the host picker changes; IME composition does
not commit.

**Not covered by tests, done by hand.** The reboot path (`tmux kill-server` →
rebuild all three agents, including "uncheck the resume row → expect a bare
shell"). The user runs this themselves.

---

## 8. Compatibility

Phase 1 adds an endpoint and two columns; it changes no broadcast payload, so
an SPA and a daemon at different versions behave exactly as they do today — the
older side never asks, or answers 404. Phase 2 is SPA-only apart from the probe
endpoint, whose 404 renders as `unverifiable`.

---

## 9. Limits (surface in UI copy where the user can see the consequence)

- **An agent that has never sent a hook is invisible.** If a nested child has a
  frame and its parent does not, the query names the child. This is the same
  frameless-ancestor acceptance the shipped design makes (v1 §4.3), and it is
  narrower here — the query runs at attach time and on hook broadcasts, by
  which point a parent that is running normally has emitted events of its own —
  but it is **not eliminated**. A wrong record is visible in the panel and
  editable.
- **Templates are global, hosts are not.** One set applies to every host; only
  the *test* is per-host.
- **The test approximates the pane's shell; it does not reproduce it.** No tty,
  no tmux `default-command`, and it runs on the host the user picked rather than
  in the pane the rebuild will create. Note for bash users: a login shell reads
  `.bash_profile` / `.bash_login` / `.profile` and **not** `.bashrc`, so a
  function defined only in `.bashrc` will fail the probe — and will equally be
  missing from the tmux pane, which is why the probe uses a login shell rather
  than papering over it.
- **A pane whose agent has exited gets no answer.** The query reads live
  frames; the pane rebuilds as a shell.
- **A confirmed record is never revisited.** Once a pane has an agent and is
  not `unverified` it stops asking, so two things do not propagate: a `cwd` the
  daemon learns after the agent was recorded (the record keeps its probed one),
  and an opencode in-process switch to a different session id (§3.3) — the
  daemon's column follows it, the record does not, because `unverified` is
  raised only by a disagreement in agent **type**. Both are visible and
  editable in the panel. This is the price of terminating (§5.5), and it is the
  same bound the shipped design already lives under.
- **Two root agents in one tmux session:** the `SessionStart` writer records the
  most recent `SessionStart`, the query returns the most recently *seen* frame.
  They can name different agents; both are real roots of the session (§5.3).

---

## 10. Review disposition

### 10.1 Round 4 — codex `task-mtrh5xai-ipphk9` on v4 — **approved for plan**

No Blocker. Two Important and one Minor, all assigned to the plan:

| # | Finding | Disposition |
|---|---|---|
| 1 | Important — the in-flight branch of §5.4.1 does not say whether the deferred run fires immediately on completion or waits for the new deadline; "fire on completion" would let a slow request with continuous hooks run back-to-back | **Plan item.** The plan defines one scheduling entry point: in flight → set `pending` only; on completion → set `nextAllowedAt`; `pending` survives to that deadline and is consumed only when a request actually starts, after every guard. The timer callback re-checks in-flight, deadline and `disowned` |
| 2 | Important — the shared walker cannot simply reuse the existing loop's return and completion conditions: it starts from the PPID (so `frame.PID == panePID` must be handled before the loop) and returns early on a framed ancestor (so `panePID` may not have been reached — which is fine, the frame is already not a root) | **Plan item.** The plan pins the walker's stop conditions and result type, with tests for self-equals-panePID, early ancestor hit, and a pane at the depth cap. The depth question itself is now measured (§3.5) |
| 3 | Minor — three leftovers from withdrawn contracts (`kind` in the rejection example, a late-cwd refresh example, "three modes") | **Fixed in this revision** |

Also confirmed by that round and folded in: `unverified` is set only by the
replay branch, which is what makes `confirm` converge (§5.4.1); and the typed
struct's "no allocation" claim is scoped to not materialising the unknown
value, not to a zero-allocation `Unmarshal` (§7's benchmark asserts the former).

### 10.2 Round 3 — codex `task-mtrgvcef-75s6gi` on v3

| # | Finding | Disposition |
|---|---|---|
| 1 | Blocker — a cooldown drops the one hook that mattered; a debounce would starve a busy session | **Accepted.** §5.4.1 is a defer-never-drop state machine with a fixed `nextAllowedAt`, a coalesced `pending` run, re-checks before the deferred request, and a termination argument. The single-hook timing is now a named test |
| 2 | Important — a successful fill removes eligibility, so the promised late-`cwd` refresh was unreachable | **Accepted by withdrawal.** The refresh mode and the promise are removed rather than widening eligibility, which would make every verified pane ask forever. Stated as a limit (§5.5, §9) |
| 3 | Important — the modes were not mutually exclusive; `unverified` never converged | **Accepted.** An ordered four-row table, plus the new **confirm** mode that clears `unverified` on an agreeing answer — the rule that makes the loop terminate (§5.5) |
| 4 | Important — `pidAncestorIncludesFn` bypasses the memo and the test seam, and has no cap | **Accepted.** It is no longer called; step 4 of §5.3 answers both questions in the one shared, memoized traversal. The deadline is honestly scoped to "between reads" |
| 5 | Important — `map[string]json.RawMessage` still scans and copies the big field | **Accepted.** A two-field typed struct, with a `benchmem` benchmark (§5.2, §7) |
| 6 | Minor — `command -v` output is not "path or word"; an alias prints its definition, a relative PATH entry a relative path | **Accepted.** `kind` is removed from the API entirely; the endpoint returns `resolved` plus what the shell printed and claims nothing about species (§4.4) |
| 7 | Minor — `Upsert` is `ON CONFLICT DO UPDATE` then re-`SELECT`; the method table was wrong and incomplete | **Accepted.** The table is rewritten against the real code and covers every `FramesStore` method (§5.2) |

### 10.3 Round 2 — codex `task-mtrggpct-ub3biv` on v2

| # | Finding | Disposition |
|---|---|---|
| 1 | Blocker — no guarantee the query runs after ancestry settles, and no reliable re-query | **Split.** The re-query half is accepted and fixed: a third trigger on hook broadcasts, with cooldowns (§5.4) — this was the likely everyday failure. The ancestry half is **not claimed as solved**; it is the frameless-ancestor limit, now stated as such (§9) instead of being marked resolved |
| 2 | Blocker — only alive + start-time; misses the pane-tree descendant check every event passes | **Accepted.** Step 3 of §5.3 reuses `resolvePanePIDFn` + `pidAncestorIncludesFn`, with the reused-pane-id case as its own test (§7) |
| 3 | Important — the parse gate freezes an old conversation and blocks a late `cwd` | **Accepted.** Gate removed; every own-frame event contributes a non-empty value, made affordable by a `map[string]json.RawMessage` extraction (§5.2) |
| 4 | Important — ordinary events go through `UpdateHookPath`, not `Upsert`; CAS re-application | **Accepted.** A per-method write contract, and a dedicated `UpdateSessionIdentity` so identity never rides a read-modify-write (§5.2) |
| 5 | Important — correcting the agent while keeping the old agent's cwd | **Accepted.** The **replace** mode writes the whole group (§5.5) |
| 6 | Important — override policy incomplete; Phase 1 overwrites a hand-typed command | **Accepted.** Replace-mode clears the override; Phase 1 writes a composed command only into an empty field; the policy is stated as policy, not proof (§4.3, §5.5) |
| 7 | Important — a create-failed operation still pinned the display | **Accepted.** Pinning applies only in flight or once `op.created` exists (§4.3) |
| 8 | Important — "most recent wins" silently changed meaning | **Accepted.** Stated as a new rule with the divergence spelled out (§5.3, §9) |
| 9 | Important — re-ask cost and stop conditions undefined; 4 `ps` forks per process read | **Accepted.** Per-request memoization + deadline (§5.3), cooldowns and an oscillation guard (§5.4), measured cost recorded (§3.4) |
| 10 | Important — no implementable `kind` contract; `type` output differs per shell | **Accepted.** `command -v`, last non-empty line, two structural kinds (§4.4) |
| 11 | Minor — imprecise summary of `disowned` and the generation comparisons | **Accepted.** §5.4 names the helpers and separates eligibility from write authorisation |
| 12 | Minor — a test result survived a host switch | **Accepted.** Keyed by `(hostId, commandWord)` (§4.5) |

### 10.4 Round 1 — codex `task-mtre55dl-hnlmvd` on v1

Findings 1 and 2 (Blockers) drove the switch from a pushed envelope to a query
(§5.1); 3 and 5 became moot with the envelope and the arming map; 4, 6, 7, 8,
9, 10, 11 and 12 were adopted and survive in v3 as the corresponding sections.

---

## 11. Deferred

- **Per-host template overrides** — only if the global set proves wrong.
- **Launch-flag reconstruction from argv** — v1 §9.2, unchanged.
- **Unifying the two identity readers** — `result.Detail` for the `SessionStart`
  envelope, `SessionIdentifier` for the frame column. Two readers of one fact,
  kept apart so Phase 1 does not perturb shipped `SessionStart` behaviour.
- **A cheaper process reader.** The ancestry walk needs only PPID, and
  `readProcessPPID` already exists at one `ps` instead of four; switching
  `classifyAncestor` to it would cut the hot path too, but it moves a shipped
  test seam and belongs in its own change.
