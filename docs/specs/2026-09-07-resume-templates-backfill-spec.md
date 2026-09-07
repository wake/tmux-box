# Spec — Resume command templates & provenance backfill

**Status:** draft v1
**Follows:** `docs/specs/2026-09-07-tab-rebuild-spec.md` (shipped alpha.332)
**Supersedes:** that spec's §9.1 "Daemon-side backfill (cut from v1)"

Two follow-ups to Tab Rebuild. They share one decision — *who is the authority
for the resume command* — so they share one spec, but they are independent
phases and could ship separately.

| | Problem | Fix |
|---|---|---|
| **A** | The composed `claude --resume <id>` calls the wrong executable for a user who launches Claude Code through a shell function | Per-agent command **templates** in Settings + a per-pane override, resolved at display and send time |
| **B** | A rebuild record is written only by a qualifying `SessionStart`, so every session that predates the deploy rebuilds as a bare shell | **Backfill** the agent group from any later owner event, once |

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

The v1 spec deferred a fix to §9.1 as a `session_meta`-backed daemon backfill
and documented why that is expensive: `meta.go:137` is UPDATE-only, external
sessions may have no row, `SetMeta` writers would have to preserve the new
columns, nil-means-no-change would let a previous generation's id survive under
a fresh instance stamp, and orphan cleanup does not fire for a reused id. None
of that is needed if the backfill rides a **live event** instead of stored
state: the event already carries the generation, and there is no row to keep
consistent.

---

## 2. Goals / non-goals

### Goals

- A user who launches an agent through a wrapper gets a working Rebuild without
  editing every pane.
- Saving a template can be **verified**: the user finds out at save time that
  the command is resolvable, rather than at rebuild time when the session is
  already gone.
- A session that was running before the feature shipped acquires an agent
  record the next time it does anything.
- Defaults reproduce today's behaviour exactly. A user who configures nothing
  sees no change.
- "What the panel shows is what gets sent" survives (v1 §4.9).

### Non-goals

- Per-host templates. One global set; the *test* runs against a chosen host
  (§4.4). Stated as a limit (§8).
- Reconstructing original launch flags from argv (v1 §9.2, still deferred).
- Templates for anything but the resume command.
- Running the template to verify it. Only the command word is resolved (§4.4);
  executing `claude --resume …` to check it would start an agent.
- Backfill from stored state. Only live events (§1.2).

---

## 3. Evidence (measured on this machine, 2026-09-07)

### 3.1 Every hook event carries the session id, not just `SessionStart`

Read from `~/.config/pdx/agent_events.db`. Since alpha.330 the step-level
`payload_json` is deduplicated and empty, so the payloads live in
`agent_trace_chains.root_payload_json`.

| Agent | Event | `session_id` | `cwd` |
|---|---|---|---|
| cc | `PdxPreToolUse` | ✅ | ✅ |
| cc | `PdxUserPromptSubmit` | ✅ | ✅ |
| cc | `PdxStop` | ✅ | ✅ |
| codex | `PdxPreToolUse` | ✅ (+ `turn_id`) | ✅ |
| codex | `PdxUserPromptSubmit` | ✅ | ✅ |
| codex | `PdxStop` | ✅ | ✅ |
| opencode | `PdxUserPromptSubmit` | ✅ | ❌ — see §3.3 |
| opencode | `PdxStop` | ✅ | ❌ — see §3.3 |

### 3.2 Event mix, and which events actually reach the frame layer

Chain counts over the current trace database:

```
cc     PdxPreToolUse   3918      codex  PdxPreToolUse       675
cc     PdxPostToolUse  3805      codex  PdxUserPromptSubmit 108
cc     PdxSubagentStop  650      codex  PdxSessionStart     102
cc     PdxUserPromptSubmit 219   codex  PdxStop              99
cc     PdxStop          130      opencode  (3 events total)
cc     PdxSessionStart    8
```

The 230:1 ratio that motivated "do not emit on every event" is real, but its
largest term never reaches the provenance gate: **cc's `PdxPreToolUse` and
`PdxPostToolUseFailure` bypass `applyFrameEvent` entirely**
(`handler.go:369-411`, the detail-only short-circuit added by PR #829 so a tool
precursor cannot resurrect a torn-down frame). `PdxPostToolUse` (3805) does
reach it. So the gate still has to throttle, but it does **not** have to reach
into a code path that was deliberately kept frame-free — the backfill hooks the
same final return site the `SessionStart` envelope already uses.

### 3.3 OpenCode's non-`SessionStart` emits omit `cwd`

`internal/agent/opencode/plugin_template.go` calls `pdxCwd()` only for
`PdxSessionStart` (line 108). `PdxStop` (145) and `PdxUserPromptSubmit` (177)
send `session_id` alone. Adding `cwd: pdxCwd()` to those two emits is a
one-line change each and keeps the backfill's cwd handling uniform across
agents.

### 3.4 A shell function is invisible to a non-interactive shell

`cld-yolo` is defined in the user's `~/.zshrc`. It is not on `PATH` and
`command -v cld-yolo` from a non-interactive shell finds nothing. It resolves
only in an interactive shell that has sourced the rc file — which is exactly
what the rebuild engine gets, because it delivers the command with tmux
`send-keys` into an interactive shell. Any save-time verification must
therefore use an **interactive login shell**, or it will report a false
negative on the very case this feature exists for.

---

## 4. Design — A: templates

### 4.1 Data model

**Settings (new store, `spa/src/stores/useResumeTemplateStore.ts`).** Modelled
on `useNotificationSettingsStore`: per-agent record, `purdexStorage`,
registered with `syncManager`.

```ts
export interface ResumeTemplatePair {
  /** Used when the record has a usable session id. Must contain `{id}`. */
  exact: string
  /** Used when it does not. Must not contain `{id}`. */
  fallback: string
}

interface ResumeTemplateState {
  agents: Record<string, ResumeTemplatePair>   // sparse: only customised agents
  getTemplates: (agentType: string) => ResumeTemplatePair   // falls back to DEFAULTS
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
+   * Never written automatically — an agent-group write clears it (§4.3).
+   */
+  resumeCommandOverride?: string
```

`resumeCommand` is **removed**, not repurposed (user decision, 2026-09-07).
Repurposing would silently promote every already-persisted auto-composed string
into an override, pinning every existing record to the old shape so the
template could never apply. Per the alpha convention
(`feedback_no_alpha_migration`) no migration is written; a stale
`resumeCommand` key left in persisted state is simply ignored, and any manual
edit made before this change is lost. `RebuildPatch`'s `field` arm renames
`'resumeCommand'` → `'resumeCommandOverride'` accordingly.

### 4.2 Resolution — three layers, one function

```ts
// spa/src/lib/rebuild/composer.ts
export function resolveResumeCommand(
  record: Pick<PaneRebuildRecord, 'agent' | 'resumeCommandOverride'> | undefined,
  templates: ResumeTemplateLookup,
): string
```

1. `record.resumeCommandOverride` is a non-empty string → return it verbatim.
2. `record.agent?.type` has templates → `sessionId` passes `SAFE_SESSION_ID` →
   `exact` with `{id}` substituted; otherwise `fallback`.
3. Otherwise → `''`.

`SAFE_SESSION_ID` (`/^[A-Za-z0-9_-]{1,128}$/`) is retained unchanged and stays
the only thing ever interpolated into a template. An id outside the alphabet
degrades to `fallback`, never to an interpolated command — the property the v1
composer had, kept.

`{id}` is the only placeholder. `{{` is not an escape; a template wanting a
literal brace is out of scope. Substitution replaces **every** occurrence.

**Every consumer of the old field becomes a call to this function**, so there
is exactly one place where the resolution order lives:

| Site | Today | After |
|---|---|---|
| `useAgentStore.ts:94` | composes and stores `resumeCommand` | no longer stores a command at all |
| `RebuildActionSet.tsx:229,294` | `record.resumeCommand` | `resolveResumeCommand(...)` |
| `RenamePopover.tsx:149` | same | same |
| `batch.ts:68,82` | same | same |
| `engine.ts:498` | same | same (resolved **once** at operation start, then pinned in the op — §4.3) |
| `SnapshotSettingsSection.tsx:664` | same | same |

### 4.3 Interaction with the existing writer ranking

v1 §4.1's ranking is unchanged in spirit; the agent-group row loses a field and
gains a clear:

| Writer | Effect on the resume command |
|---|---|
| Qualifying `SessionStart` | **Clears `resumeCommandOverride`.** The override was written against the previous session id; keeping it would send a stale id under a new agent group. "Automatic values always win" (v1 §4.1) already said this — with a stored composed string it was implicit, now it must be explicit. |
| Backfill (§5) | Same, for the same reason — it writes an agent group. |
| User edit | Sets `resumeCommandOverride` to the submitted text; an empty submission **clears** it and the row falls back to the template. |
| Template change in Settings | Affects every pane that has no override, retroactively, including dead ones. That is the point of the feature. |

**"What you see is what gets sent" (v1 §4.9) is preserved** because display and
send read the same function. The engine resolves once at operation start and
pins the string into the rebuild operation (`engine.ts:502` already stores
`resumeCommand` on the op), so a template edited mid-rebuild cannot change what
a retry sends.

### 4.4 Save-time verification

**Why it exists.** §3.4: the failure this feature fixes is a *path* failure,
and a path failure is invisible until the session is already gone. The user
asked for the test explicitly.

**What is verified.** The **command word only** — the first whitespace-
separated token of the template, with `{id}` never substituted. Running the
whole template would launch an agent (§2 non-goals).

**Endpoint.** `POST /api/shell/resolve-command`, in the session module
(`internal/module/session/`), which already owns shell and tmux execution.

```
request   { "command": "cld-yolo" }
200       { "resolved": true,  "kind": "function", "detail": "cld-yolo is a shell function" }
200       { "resolved": true,  "kind": "file",     "detail": "/Users/wake/.local/bin/claude" }
200       { "resolved": false, "kind": "",         "detail": "" }
200       { "resolved": false, "kind": "unverifiable", "reason": "shell_metacharacters" }
400       validation failure (empty / oversized / non-string)
504       the probe exceeded its deadline
```

**How.** The user's login shell, interactively, with the token passed as a
positional parameter — never interpolated into the script text:

```go
exec.CommandContext(ctx, shell, "-i", "-c", `type -- "$1"`, "_", token)
```

- `shell` is `$SHELL`, falling back to the passwd shell, falling back to
  `/bin/sh`. This is the same shell tmux starts (`default-shell`), which is the
  shell that will actually receive the send-keys line.
- `-i` is load-bearing: without it a shell function is invisible (§3.4).
- `ctx` carries a **5 s deadline**; a slow or input-hungry rc file must not
  wedge the daemon. stdin is `/dev/null`; stdout/stderr are captured and
  truncated to 512 bytes.
- Exit 0 → `resolved: true`. `kind` is derived from the output text
  (`function` / `alias` / `builtin` / `file`), best-effort and display-only;
  `resolved` is the value the UI branches on.
- **Rejected before exec** (`resolved: false, kind: "unverifiable"`): a token
  longer than 256 bytes, or containing any of ``| & ; < > ( ) $ ` \ " ' newline``
  or a leading `-`. Those are not command words we can meaningfully resolve,
  and refusing is a stated result rather than a silent pass. The template
  itself is *not* restricted — only what we agree to probe.

The probe never rejects a **save**. It reports; the user decides. A red "not
found" next to a saved template is information, not a block — the command may
exist on another host, or may be about to.

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
  OpenCode
    …
                                                        [Reset all to defaults]
```

- Agent rows come from the same registry the rest of the settings use
  (`AGENT_NAMES`), so a fourth agent appears without touching this file.
- Editing reuses the `EditableCwdCell` pattern (alpha.324): `committedRef`
  against double submit, `disabled` while busy, and `compositionRef` +
  `isComposing` so an IME Enter does not commit. Those were review findings on
  that component; rediscovering them here is not acceptable.
- **Inline validation, non-blocking**: `exact` without `{id}`, or `fallback`
  with `{id}`, shows a warning and still saves. A saved `exact` without `{id}`
  simply never interpolates — it is a valid, if odd, choice, and blocking the
  save would be a worse failure mode than a warning.
- The Test button probes the **command word of that row**, against the host in
  the picker, and renders the result inline until the row is edited again. The
  host picker exists because templates are global and hosts are not (§8).
- All copy goes through i18n; no literal English in the component.

---

## 5. Design — B: backfill

### 5.1 The envelope gains a source

```diff
 "pdx_provenance": {
   "owner_session_start": true,
+  "source": "session_start" | "backfill",
   "agent_type": "codex",
   "session_id": "01a07ace-…",
   "cwd": "/Users/wake/Workspace/wake/purdex",
   "tmux_pane_id": "%2",
   "tmux_instance": "4471:1788740000"
 }
```

`owner_session_start` keeps its meaning — *the sender owns this pane* — and is
`true` for both. `source` selects the SPA's write policy, and it must be
explicit rather than inferred: a `SessionStart` envelope **overwrites** the
agent group, a backfill envelope **fills only when there is no agent**. An
absent `source` is read as `session_start`.

### 5.2 Daemon — where the backfill is granted

Same site as the existing gate, `applyFrameEvent`'s `created_frame` /
`updated_frame` return (`frame_ops.go:898-920`). Everything that returns
earlier — the proxy fast-path, the post-Upsert canonicalization, the subagent
paths, `skipped` — grants nothing, which is the fail-safe the v1 design already
relies on (memory: "envelope 掛在變更結果、不是預先判定").

```go
var prov *Provenance
switch {
case lifecycle == agentpkg.LifecycleSessionStart && !req.SenderUncertain &&
     verdict == VerdictRoot && stored.ParentFrameID == "":
    p := buildProvenance(req, result, m.sessionTmuxInstance(), ProvenanceSourceSessionStart)
    prov = &p
default:
    prov = m.tryBackfillProvenance(req, stored, lifecycle)
}
```

`tryBackfillProvenance` returns non-nil only when **all** hold, checked in this
order — cheapest first, so the walk is the last thing tried:

1. `lifecycle != LifecycleSessionStart` (that case is handled above and must
   not be double-granted) and `!req.SenderUncertain`.
2. `stored.ParentFrameID == ""` and `stored` is the sender's own frame.
3. The provider yields a session id from the raw event (§5.3). No id → nothing
   to backfill.
4. The arming map has no entry for `frameID + ":" + sessionID` (§5.4).
5. `m.classifyAncestor(req) == VerdictRoot`.

Condition 5 is the same ownership standard the `SessionStart` gate uses, and it
is what keeps nesting correct without new machinery: a nested cc-in-cc's later
event finds the parent's frame and returns `VerdictSameTypeAbove`; a nested
codex-in-cc whose `SessionStart` was proxy-collapsed finds the live cc frame and
returns `VerdictProxyParent`. Neither backfills. `VerdictIndeterminate` never
writes — the rule that runs through the whole feature is *no evidence, no
action*.

On success the map is marked, so condition 4 makes the walk run **at most once
per (frame, session id) per arming window**. Every other event pays a mutex and
a map lookup.

**Ownership stays in the frame layer** (memory, "三件別翻案的設計決定" #1). No
process-ancestry classifier, no new `ps` call beyond the one walk.

### 5.3 Daemon — reading the identity off the raw event

The `SessionStart` path reads `result.Detail`, which the providers populate per
status branch. Extending that to every branch of three providers would perturb
status semantics for no benefit. Instead, an optional provider interface reads
the raw payload directly — `req.RawEvent` is already on the request:

```go
// internal/agent/provider.go
type SessionIdentifier interface {
    // IdentifyEvent extracts the sender's own session id and cwd from a raw
    // hook payload. Returns ("", "") when the event carries neither.
    IdentifyEvent(purdexName string, rawEvent json.RawMessage) (sessionID, cwd string)
}
```

Implemented by cc, codex and opencode over the shared `session_id` / `cwd`
keys (§3.1). A provider that does not implement it never backfills. The
`SessionStart` path is left on `result.Detail` unchanged — no regression risk
on shipped behaviour, at the cost of two readers for one fact, which is noted
here so a later cleanup is a deliberate choice rather than a rediscovery.

**OpenCode plugin change.** `plugin_template.go` adds `cwd: pdxCwd()` to the
`PdxStop` and `PdxUserPromptSubmit` emits (§3.3). The existing `parentID`
child-session filter is untouched — v1 §9.3 states it is a precondition of the
ownership invariant for opencode and must not be dropped.

### 5.4 Daemon — the arming window

```go
// Module fields
backfilled map[string]struct{}   // guarded by an existing mutex
```

- **Marked** when a backfill envelope is granted.
- **Cleared** in `sendSnapshot` (`module.go:552`), which runs for every new WS
  subscriber. A fresh SPA connection re-arms every frame, so a session that was
  running while nobody was watching is backfilled on the connected user's next
  interaction with it (user decision, 2026-09-07).
- Not persisted. A daemon restart re-arms, which costs one walk per frame.

Bounded by live frames × distinct session ids observed since the last arming,
and truncated at every reconnect. If it ever needs a cap, the cap is a follow-up
issue, not v1 scope.

### 5.5 SPA — the fill-only write

`parseProvenance` gains `source` (defaulting to `'session_start'`); everything
else about it is unchanged, including the rule that an envelope with no
`tmux_instance` is rejected outright.

`writeProvenanceRecord` routes on it:

```ts
kind: prov.source === 'backfill' ? 'agent-group-backfill' : 'agent-group'
```

The new `RebuildPatch` arm, applied in `useTabStore.setPaneRebuild`:

- **No-op** when the pane's record already has `agent`. This is the "有了就跳過"
  policy, and it lives on the SPA because only the SPA knows what the record
  holds.
- Otherwise writes `agent` (type, sessionId, tmuxPaneId, updatedAt), clears
  `resumeCommandOverride` (§4.3), and sets `capturedAt`.
- **Never clears a field it does not carry.** Unlike `agent-group`, a backfill
  is a fill: it writes `cwd` only when it has one *and* the existing `cwd` is
  absent or was `'pane-probe'`. An agent-reported cwd outranks a probe; a
  probe outranks nothing.
- `cwdSource` gains a third value `'agent-backfill'`, distinct from
  `'agent-session-start'` so the provenance of the value stays honest.

The generation guard is unchanged: the write matches panes on
`(hostId, sessionCode, tmuxInstance)` and the envelope's `tmux_instance` is the
daemon's own answer (v1 §4.5, §4.6).

A later qualifying `SessionStart` overwrites a backfilled group wholesale, as
it does any other — automatic values still win.

### 5.6 What this replaces

v1 §9.1's deferred `session_meta` backfill is **cancelled, not postponed**, and
that section is rewritten to say so. The live-event route needs no stored
state, so none of §9.1's four objections apply: there is no row to upsert, no
`SetMeta` writer to teach, no nil-means-no-change ambiguity, and no orphan
cleanup to fire. The other two §9.1 degradations are unchanged: a session that
never emits another owner event still rebuilds as a shell, and the `unverified`
flag still marks a record whose agent disagrees with the projection.

---

## 6. Phases

| Phase | Content | Reviewable on its own |
|---|---|---|
| **1** | B — backfill. Daemon: `source` on the envelope, `SessionIdentifier`, `tryBackfillProvenance`, arming window, opencode `cwd` emits. SPA: `parseProvenance.source`, `agent-group-backfill` patch, `cwdSource: 'agent-backfill'`. | Yes — no UI surface, fully covered by Go + store tests |
| **2** | A — templates. `useResumeTemplateStore`, `resolveResumeCommand`, the `resumeCommand` → `resumeCommandOverride` rename across all six consumers, `POST /api/shell/resolve-command`, `ResumeTemplateSettings.tsx`, i18n. | Yes |

Phase 1 first: it is the coverage bug the user has actually seen, and it does
not touch any of the files Phase 2 rewrites.

---

## 7. Testing strategy

**Go — backfill (Phase 1).** Extending `provenance_test.go`, whose
`newProvenanceTestModule` harness already drives `applyFrameEvent` →
`buildProjectionNormalized` → `attachProvenance`:

- A non-`SessionStart` owner event on a root frame emits `source: "backfill"`.
- A second such event for the same (frame, session id) emits **nothing**.
- After `sendSnapshot`, the same event emits again.
- A different session id on the same frame emits again.
- Nested same-type (`VerdictSameTypeAbove`) → no envelope.
- Nested cross-type (`VerdictProxyParent`) → no envelope; the existing
  proxy-collapse assertions still hold.
- `VerdictIndeterminate` → no envelope.
- `SenderUncertain` → no envelope.
- An event with no session id → no envelope, and **no `classifyAncestor`
  call** (asserted through the `readProcessInfoFn` seam the existing tests
  already use to count reads).
- A `SessionStart` still emits `source: "session_start"` and still overwrites —
  every assertion in the existing file keeps passing unchanged.

**Go — probe (Phase 2).** `resolve-command` handler: metacharacter rejection
without exec, oversize rejection, deadline → 504, exit-0 → `resolved: true`,
exit-1 → `resolved: false`. The shell invocation goes behind a function
variable so tests substitute a stub; one integration test runs the real
`$SHELL` against a token guaranteed to exist (`cd`, a builtin).

**Bun.** `plugin_template_bun_integration_test.go` gains an assertion that
`PdxStop` and `PdxUserPromptSubmit` emit `cwd`, alongside the existing
assertion pinning the `parentID` child filter.

**Vitest — store (Phase 1).** `agent-group-backfill` no-ops when `agent`
exists; fills when it does not; does not clear a probe cwd it has no
replacement for; replaces a probe cwd when it does; obeys the generation guard;
a later `agent-group` overwrites it.

**Vitest — resolution (Phase 2).** `resolveResumeCommand` across the three
layers × (usable id / unusable id / no id) × (override / no override);
`{id}` substituted everywhere it appears; an unsafe id degrades to `fallback`
and is never interpolated; an unknown agent yields `''`.

**Vitest — UI (Phase 2).** The panel renders the *composed* string, not the
template; editing the row writes an override; clearing it restores the
template; changing a template updates a pane with no override and leaves an
overridden pane alone; the Test button renders each of the four probe outcomes;
IME composition does not commit.

**Not covered by tests, must be done by hand.** The reboot path
(`tmux kill-server` → rebuild all three agents, including the
"uncheck the resume row → expect a bare shell" case). Carried over from the v1
plan; the user has said they will run it themselves.

---

## 8. Limits (surface in UI copy)

- **Templates are global, hosts are not.** One template set applies to every
  host; only the *test* is per-host. A wrapper that exists on one machine and
  not another will resolve on one and fail on the other, and the Test button is
  how the user finds that out.
- **The test verifies the command word, not the command.** Arguments, flags and
  `{id}` are not checked, and a resolvable command can still fail at run time.
- **A record with no agent still rebuilds as a shell.** The backfill needs one
  owner event; a session that is idle and never touched again never gets one.
- **One agent group per record** (v1 §4.4) — unchanged.

---

## 9. Deferred

- **Per-host template overrides.** Only if the global set proves wrong in
  practice.
- **Launch-flag reconstruction from argv** — v1 §9.2, unchanged.
- **A cap on the arming map** (§5.4).
- **Unifying the two identity readers** — `result.Detail` for `SessionStart`,
  `SessionIdentifier` for backfill (§5.3).
