# Tab Rebuild Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give every `tmux-session` pane a self-maintaining record of its tmux
session name, launch cwd, running agent and resume command, so a pane whose
session died to a host reboot can be recreated with one button.

**Architecture:** The daemon already receives everything needed inside hook
payloads and already decides which agent owns a pane; both are currently
dropped before reaching the SPA. Phase 1–2 surface them as a self-contained
`pdx_provenance` envelope gated on the frame-mutation outcome, and stamp the
tmux generation onto the session payloads. Phase 3–4 make the SPA
generation-aware and accumulate the record in the persisted tab store. Phase
5–7 build the rebuild engine and its three entry points.

**Tech Stack:** Go (net/http, modernc.org/sqlite, gorilla/websocket) · React 19
/ Zustand 5 / Vitest / Tailwind 4 · pnpm

**Spec:** `docs/specs/2026-09-07-tab-rebuild-spec.md` (v4)

## Global Constraints

- **TDD, no exceptions.** Every task writes the failing test first, runs it to
  see it fail, then implements. Each task is one commit.
- **Commit messages in English**; conversation replies in Traditional Chinese
  (project convention). Every commit ends with:
  ```
  Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01X87wCa7vVUw4yAH1b5PPfj
  ```
- **Verification commands** (the root `package.json` has no `lint`/`build`
  scripts — these exact forms are required):
  ```
  go test ./...
  pnpm --prefix spa exec vitest run
  pnpm --prefix spa run lint
  pnpm --prefix spa run build
  ```
- **No persist migration.** Alpha convention: `rebuild` is an optional field;
  panes without it must behave exactly as today.
- **Never widen an existing behaviour silently.** Phase 1 is a pure refactor:
  if any existing test in `internal/module/agent` needs editing to pass, stop
  and report instead of editing it.
- **i18n**: every user-visible string goes in BOTH `spa/src/locales/en.json`
  and `spa/src/locales/zh-TW.json`. Keys are flat dotted strings, e.g.
  `"rebuild.create_session"`.
- **Stream-mode panes (`mode: 'stream'`) are out of scope** in every task.
  Guard on `content.mode === 'terminal'` wherever panes are selected.
- **Go test packages are mixed — check before copying a snippet.** The
  `internal/agent/{cc,codex,opencode}` status tests are *external*
  (`package cc_test` etc.) and cannot call unexported functions such as
  `deriveCCStatus`; go through `Provider.DeriveStatus` via each file's existing
  helper (`deriveViaProvider` in cc/opencode, `deriveWithRaw` in codex).
  `internal/module/agent` and `internal/module/session` tests are *internal*
  (`package agent` / `package session`) and may call unexported symbols
  directly, which is what Tasks 1, 4 and 5 rely on. The opencode
  plugin-template tests are also internal (`package opencode`).

---

## File Structure

**Daemon (Go)**

| File | Responsibility |
|---|---|
| `internal/module/agent/ancestor.go` *(new)* | `AncestorVerdict` + `classifyAncestor` — the single pane-ancestor traversal |
| `internal/module/agent/frame_ops.go` *(modify)* | `findProxyParent` becomes a thin caller; envelope gated on mutation outcome |
| `internal/module/agent/provenance.go` *(new, Task 5)* | Builds the `pdx_provenance` map from a request + mutation outcome |
| `internal/agent/{cc,codex,opencode}/status.go` *(modify)* | `SessionStart` passes `session_id` / `cwd` through `Detail` |
| `internal/agent/opencode/plugin_template.go` *(modify)* | Plugin emits `cwd` |
| `internal/module/session/{provider,service}.go` *(modify)* | `SessionInfo.TmuxInstance` |
| `internal/module/session/watcher.go` *(modify)* | Instance read per tick and hashed with the list |

**SPA (TypeScript)**

| File | Responsibility |
|---|---|
| `spa/src/types/tab.ts` *(modify)* | `PaneRebuildRecord`, `rebuild?` on the `tmux-session` variant |
| `spa/src/stores/useTabStore.ts` *(modify)* | `setPaneRebuild`, `markTerminatedForGeneration` |
| `spa/src/lib/rebuild/composer.ts` *(new)* | Pure: agent + session id → resume command |
| `spa/src/lib/rebuild/provenance.ts` *(new)* | Pure: envelope → record patch; validation |
| `spa/src/lib/rebuild/engine.ts` *(new)* | The rebuild operation (create → resume → re-point) |
| `spa/src/lib/rebuild/transport.ts` *(new)* | Host-pinned fetch, no active-host fallback |
| `spa/src/stores/useRebuildStore.ts` *(new)* | Per-pane operation state + the global operation lock |
| `spa/src/components/TerminatedPane.tsx` *(modify)* | Action set UI |
| `spa/src/components/RebuildActionSet.tsx` *(new)* | The three editable rows, reused by pane + popover |
| `spa/src/components/RenamePopover.tsx` *(modify)* | Per-pane detail blocks |
| `spa/src/features/workspace/hooks.ts` *(modify)* | Popover entry point collects all terminal panes |
| `spa/src/components/settings/SnapshotSettingsSection.tsx` *(modify)* | Batch view + Rebuild all + shared lock |

---

# Phase 1 — Ancestor classification (daemon, pure refactor)

### Task 1: Split the ancestor walk's overloaded return

**Files:**
- Create: `internal/module/agent/ancestor.go`
- Create: `internal/module/agent/ancestor_test.go`
- Modify: `internal/module/agent/frame_ops.go` (`findProxyParent`, 1963-2035;
  1963 is the doc comment, 1972 the `func` line, 2030 the self-parent guard)

**Interfaces:**
- Consumes: `EventRequest` (`handler.go:85-103`), `store.Frame`,
  `m.frames.FindByPanePID`, `readProcessInfoFn`, `isPidAliveFn`,
  `processStartTimeFn`, `proxyMaxDepth` — all existing package-level symbols.
- Produces:
  ```go
  type AncestorVerdict int
  const (
      VerdictRoot          AncestorVerdict = iota
      VerdictSameTypeAbove
      VerdictProxyParent
      VerdictIndeterminate
  )
  func (m *Module) classifyAncestor(req EventRequest) (AncestorVerdict, *store.Frame, error)
  ```
  Task 4 consumes `classifyAncestor`; nothing else does.

**Context the implementer needs:** `findProxyParent` today returns
`(nil, nil)` for five distinct outcomes — no framed ancestor, same-type hard
stop, stale candidates, depth exceeded, and process-read errors. The walk is
otherwise correct and heavily tested; **do not change how it walks**. Only
record which outcome ended it. Liveness + identity gating already applies to
same-type and cross-type candidates alike (`frame_ops.go:1996-2015`), so a
*stale* same-type frame must keep the walk going — `frame_ops_test.go:1470`
pins that.

- [ ] **Step 1: Write the failing test**

```go
// internal/module/agent/ancestor_test.go
package agent

import "testing"

func TestClassifyAncestor_NoFramedAncestor_Root(t *testing.T) {
	m := newProxyTestModule(t)
	req := EventRequest{TmuxPaneID: "%5", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
	withProcessTree(t, map[int]int{200: 999}) // 999 has no frame

	verdict, parent, err := m.classifyAncestor(req)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if verdict != VerdictRoot {
		t.Fatalf("verdict = %v, want VerdictRoot", verdict)
	}
	if parent != nil {
		t.Fatalf("parent = %v, want nil", parent)
	}
}

func TestClassifyAncestor_LiveSameTypeAncestor_SameTypeAbove(t *testing.T) {
	m := newProxyTestModule(t)
	seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	req := EventRequest{TmuxPaneID: "%5", AgentType: "cc", SenderPID: 200, SenderStartTime: "t200"}
	withProcessTree(t, map[int]int{200: 100})
	withLivePids(t, map[int]string{100: "t100"})

	verdict, _, err := m.classifyAncestor(req)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if verdict != VerdictSameTypeAbove {
		t.Fatalf("verdict = %v, want VerdictSameTypeAbove", verdict)
	}
}

func TestClassifyAncestor_LiveCrossTypeAncestor_ProxyParent(t *testing.T) {
	m := newProxyTestModule(t)
	parent := seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	req := EventRequest{TmuxPaneID: "%5", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
	withProcessTree(t, map[int]int{200: 100})
	withLivePids(t, map[int]string{100: "t100"})

	verdict, got, err := m.classifyAncestor(req)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if verdict != VerdictProxyParent {
		t.Fatalf("verdict = %v, want VerdictProxyParent", verdict)
	}
	if got == nil || got.FrameID != parent.FrameID {
		t.Fatalf("parent = %v, want %v", got, parent.FrameID)
	}
}

func TestClassifyAncestor_StaleSameTypeBelowLiveCrossType_ProxyParent(t *testing.T) {
	// Regression guard mirroring frame_ops_test.go:1470 — a dead same-type
	// frame must not hard-stop the walk to a live cross-type grandparent.
	m := newProxyTestModule(t)
	seedFrame(t, m, "%5", "codex", 150, "t150", 5) // stale: pid not alive
	grand := seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	req := EventRequest{TmuxPaneID: "%5", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
	withProcessTree(t, map[int]int{200: 150, 150: 100})
	withLivePids(t, map[int]string{100: "t100"}) // 150 absent = dead

	verdict, got, err := m.classifyAncestor(req)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if verdict != VerdictProxyParent || got == nil || got.FrameID != grand.FrameID {
		t.Fatalf("verdict = %v parent = %v, want ProxyParent/%s", verdict, got, grand.FrameID)
	}
}

func TestClassifyAncestor_SelfParent_Indeterminate(t *testing.T) {
	// A process whose PPID is itself must not spin to the depth cap.
	m := newProxyTestModule(t)
	req := EventRequest{TmuxPaneID: "%5", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
	withProcessTree(t, map[int]int{200: 300, 300: 300})

	verdict, _, err := m.classifyAncestor(req)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if verdict != VerdictIndeterminate {
		t.Fatalf("verdict = %v, want VerdictIndeterminate", verdict)
	}
}

func TestClassifyAncestor_DepthCapExceeded_Indeterminate(t *testing.T) {
	m := newProxyTestModule(t)
	chain := map[int]int{}
	pid := 200
	for i := 0; i < proxyMaxDepth+3; i++ {
		chain[pid] = pid + 1
		pid++
	}
	withProcessTree(t, chain)
	req := EventRequest{TmuxPaneID: "%5", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}

	verdict, _, err := m.classifyAncestor(req)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if verdict != VerdictIndeterminate {
		t.Fatalf("verdict = %v, want VerdictIndeterminate", verdict)
	}
}

func TestClassifyAncestor_ProcessReadError_Indeterminate(t *testing.T) {
	m := newProxyTestModule(t)
	req := EventRequest{TmuxPaneID: "%5", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
	withProcessReadError(t, 200)

	verdict, _, err := m.classifyAncestor(req)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if verdict != VerdictIndeterminate {
		t.Fatalf("verdict = %v, want VerdictIndeterminate", verdict)
	}
}
```

**Note on helpers:** `newProxyTestModule` and `seedFrame` already exist in
`frame_ops_test.go`. `withProcessTree`, `withLivePids` and
`withProcessReadError` do not — write them in `ancestor_test.go` as thin
wrappers that swap `readProcessInfoFn` / `isPidAliveFn` /
`processStartTimeFn` and restore them via `t.Cleanup`, following the swap
pattern already used at `frame_ops_test.go:2540-2560`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/module/agent/ -run TestClassifyAncestor -v`
Expected: FAIL — `m.classifyAncestor undefined`, `VerdictRoot undefined`.

- [ ] **Step 3: Move the walk body into `classifyAncestor`**

Cut the loop body out of `findProxyParent` verbatim into
`internal/module/agent/ancestor.go`, replacing each `return nil, nil` with the
verdict that describes why it stopped:

```go
package agent

import "github.com/wake/purdex/internal/store"

// AncestorVerdict distinguishes the outcomes that findProxyParent used to
// collapse into (nil, nil). See spec §4.3.
type AncestorVerdict int

const (
	VerdictRoot AncestorVerdict = iota
	VerdictSameTypeAbove
	VerdictProxyParent
	VerdictIndeterminate
)

// classifyAncestor walks the sender's PPID chain (capped at proxyMaxDepth)
// looking for an alive, identity-verified frame in the same pane. It is the
// single traversal behind both the proxy decision and the provenance
// ownership decision; the two results are reported separately so neither
// shadows the other.
//
// The traversal itself is unchanged from the pre-split findProxyParent — only
// the return value carries more information.
func (m *Module) classifyAncestor(req EventRequest) (AncestorVerdict, *store.Frame, error) {
	if m.frames == nil {
		return VerdictIndeterminate, nil, nil
	}
	info, err := readProcessInfoFn(req.SenderPID)
	if err != nil {
		return VerdictIndeterminate, nil, nil
	}
	ppid := info.PPID
	for depth := 0; depth < proxyMaxDepth; depth++ {
		if ppid <= 1 {
			return VerdictRoot, nil, nil
		}
		candidate, err := m.frames.FindByPanePID(req.TmuxPaneID, ppid)
		if err != nil {
			return VerdictIndeterminate, nil, err
		}
		if candidate != nil {
			if isPidAliveFn(candidate.PID) {
				actualStart, serr := processStartTimeFn(candidate.PID)
				if serr != nil {
					return VerdictIndeterminate, nil, nil
				}
				if actualStart == candidate.ProcessStartTime {
					if candidate.AgentType == req.AgentType {
						return VerdictSameTypeAbove, candidate, nil
					}
					return VerdictProxyParent, candidate, nil
				}
			}
			// Stale candidate: keep walking (frame_ops_test.go:1470).
		}
		// No frame at this PID — walk one more level up.
		ancestorInfo, nerr := readProcessInfoFn(ppid)
		if nerr != nil {
			return VerdictIndeterminate, nil, nil
		}
		// Self-parent guard, verbatim from frame_ops.go:2030: without it the
		// loop would re-query the same PID until the depth cap.
		if ancestorInfo.PPID == ppid {
			return VerdictIndeterminate, nil, nil
		}
		ppid = ancestorInfo.PPID
	}
	return VerdictIndeterminate, nil, nil
}
```

Note the two deliberate differences from the pre-split code, both conservative:
the old code returned `(nil, nil)` for the self-parent guard and for the
depth-cap exit, which `findProxyParent` reads as "do not proxy" — unchanged —
while `classifyAncestor` reports `VerdictIndeterminate`, which suppresses
provenance rather than granting it.

Then reduce `findProxyParent` to:

```go
// findProxyParent reports the cross-type ancestor frame a SessionStart should
// be folded into, or nil when it should not fold. Contract unchanged; the
// traversal now lives in classifyAncestor.
func (m *Module) findProxyParent(req EventRequest) (*store.Frame, error) {
	verdict, parent, err := m.classifyAncestor(req)
	if err != nil {
		return nil, err
	}
	if verdict == VerdictProxyParent {
		return parent, nil
	}
	return nil, nil
}
```

- [ ] **Step 4: Run the new test and the whole existing agent suite**

Run: `go test ./internal/module/agent/ -run TestClassifyAncestor -v`
Expected: PASS

Run: `go test ./internal/module/agent/`
Expected: PASS — **every pre-existing test unchanged**. If any existing test
requires editing, the refactor is not behaviour-preserving: stop and report
rather than editing the test.

- [ ] **Step 5: Full suite + commit**

```bash
go test ./...
git add internal/module/agent/ancestor.go internal/module/agent/ancestor_test.go internal/module/agent/frame_ops.go
git commit -m "refactor(agent): split the pane-ancestor walk's overloaded return"
```

---

# Phase 2 — Provenance on the wire (daemon)

### Task 2: `SessionStart` passes `session_id` and `cwd` through `Detail`

**Files:**
- Modify: `internal/agent/cc/status.go:14-23`
- Modify: `internal/agent/codex/status.go` (`PdxSessionStart` branch)
- Modify: `internal/agent/opencode/status.go` (`PdxSessionStart` branch)
- Test: `internal/agent/cc/status_test.go`, `internal/agent/codex/status_test.go`,
  `internal/agent/opencode/status_test.go`

**Interfaces:**
- Consumes: `agent.DeriveResult` (`internal/agent/status.go:15-28`).
- Produces: `DeriveResult.Detail["session_id"]` and `Detail["cwd"]` on
  `PdxSessionStart` for all three providers. Task 4 reads them.

**Context:** observed payloads are in spec §3.1 — both cc and codex carry
`session_id` and `cwd` at the top level of `raw_event`. Keys that are absent
must stay **absent** from `Detail`, not present with a nil value: downstream
`strFromDetail` (`frame_ops.go:1136`) treats a non-string as empty, but a nil
entry would still serialize into the WS payload as `"session_id": null`.

- [ ] **Step 1: Write the failing tests**

```go
// internal/agent/cc/status_test.go
func TestDeriveCCStatus_SessionStart_CarriesProvenance(t *testing.T) {
	raw := []byte(`{"session_id":"441c80d5","cwd":"/w/csp","source":"startup","modelName":"opus"}`)
	got := deriveCCStatus("PdxSessionStart", raw)
	if !got.Valid {
		t.Fatalf("Valid = false")
	}
	if got.Detail["session_id"] != "441c80d5" {
		t.Fatalf("session_id = %v", got.Detail["session_id"])
	}
	if got.Detail["cwd"] != "/w/csp" {
		t.Fatalf("cwd = %v", got.Detail["cwd"])
	}
}

func TestDeriveCCStatus_SessionStart_OmitsAbsentKeys(t *testing.T) {
	got := deriveCCStatus("PdxSessionStart", []byte(`{"source":"startup"}`))
	if _, ok := got.Detail["session_id"]; ok {
		t.Fatalf("session_id present for a payload that has none")
	}
	if _, ok := got.Detail["cwd"]; ok {
		t.Fatalf("cwd present for a payload that has none")
	}
}

func TestDeriveCCStatus_SessionStart_CompactStillIgnored(t *testing.T) {
	got := deriveCCStatus("PdxSessionStart", []byte(`{"source":"compact","session_id":"x"}`))
	if got.Valid {
		t.Fatalf("compact must stay Valid=false")
	}
}
```

Write only the **first two** tests against `deriveCodexStatus` and
`deriveOpenCodeStatus` — provenance passthrough, and absent keys staying
absent. **Do not copy `CompactStillIgnored`**: the `source == "compact"` guard
is a cc-only rule (`internal/agent/cc/status.go:15-17`); codex and opencode
have no such branch and adding one would change their behaviour. Codex payload
per spec §3.1; opencode currently sends only `session_id` until Task 3, so its
absent-key case asserts `cwd` absent.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/agent/... -run SessionStart_Carries -v`
Expected: FAIL — `Detail` is nil.

- [ ] **Step 3: Implement a shared helper and use it in all three providers**

Add to `internal/agent/status.go`:

```go
// DetailStrings copies only the string-valued keys present in raw into a new
// map, so an absent key stays absent rather than serializing as null.
func DetailStrings(raw map[string]any, keys ...string) map[string]any {
	out := make(map[string]any, len(keys))
	for _, k := range keys {
		if v, ok := raw[k].(string); ok && v != "" {
			out[k] = v
		}
	}
	return out
}
```

In each provider's `PdxSessionStart` branch, add
`Detail: agent.DetailStrings(raw, "session_id", "cwd")` alongside the existing
fields. For cc, keep the `source == "compact"` early return **above** it.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/agent/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/status.go internal/agent/cc/status.go internal/agent/cc/status_test.go internal/agent/codex/status.go internal/agent/codex/status_test.go internal/agent/opencode/status.go internal/agent/opencode/status_test.go
git commit -m "feat(agent): carry session_id and cwd on SessionStart detail"
```

---

### Task 3: OpenCode plugin emits `cwd`

**Files:**
- Modify: `internal/agent/opencode/plugin_template.go` (the `session.created`
  case, ~line 83-93)
- Test: `internal/agent/opencode/plugin_template_contract_test.go`

**Interfaces:**
- Produces: `PdxSessionStart` payload gains `cwd`. Task 2's opencode
  passthrough then has something to pass.

**Context:** the plugin body is a Go string template rendered by
`renderManagedPlugin`. It runs inside opencode under Bun. Two sources for the
directory, in order of preference: the plugin API's `directory` argument, and
`process.cwd()` as the fallback. The template's exported function currently
takes no arguments — widen it to `async (ctx = {}) =>` and read
`ctx.directory`. The template is compared **byte-exactly** by
`CheckHooks`, so the contract test's expected string must be regenerated in
the same commit.

- [ ] **Step 1: Write the failing test**

```go
// internal/agent/opencode/plugin_template_contract_test.go
func TestPluginTemplate_SessionCreated_EmitsCwd(t *testing.T) {
	body := renderManagedPlugin("/usr/local/bin/pdx")
	if !strings.Contains(body, "cwd: pdxCwd()") {
		t.Fatalf("session.created emit does not include cwd:\n%s", body)
	}
	if !strings.Contains(body, "function pdxCwd()") {
		t.Fatalf("pdxCwd helper missing")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/agent/opencode/ -run EmitsCwd -v`
Expected: FAIL — substring not found.

- [ ] **Step 3: Implement**

In `renderManagedPlugin`, change the exported signature to
`export const PurdexOpenCodeHooks = async (ctx = {}) => {`, add the helper
next to `emit`:

```js
  function pdxCwd() {
    try {
      if (ctx && typeof ctx.directory === 'string' && ctx.directory) return ctx.directory
      if (ctx && typeof ctx.worktree === 'string' && ctx.worktree) return ctx.worktree
      return process.cwd()
    } catch {
      return ''
    }
  }
```

and change the parent-session emit to
`await emit(PURDEX_EVENT.PdxSessionStart, { session_id: sid, cwd: pdxCwd() })`.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/agent/opencode/`
Expected: PASS. Extend the bun integration test
(`plugin_template_bun_integration_test.go`) — which executes the rendered
plugin under Bun and captures the emitted stdin — with three payload
assertions rather than only the source-substring check:

- `ctx.directory` present → emitted `cwd` equals it;
- `ctx` empty → emitted `cwd` equals the process cwd (fallback);
- a **child** session (`info.parentID` set) emits `PdxSubagentStart`, not
  `PdxSessionStart` — the provider-level filter that spec §9.3 names as a
  precondition of the ownership invariant. This assertion must exist so a
  future edit cannot quietly remove the filter.

**Real-agent verification gate (spec §9.3).** cc and codex payloads were
observed; opencode's was not. Before Phase 4 consumes the opencode path, run a
real opencode session in a tmux pane on this host and read back what the daemon
actually received:

```sql
select payload_json from agent_trace_steps
where agent_type = 'opencode' and event_name like '%SessionStart%' and kind = 'trigger'
order by created_at desc limit 1;
```

against `~/.config/pdx/agent_events.db`. Confirm `session_id` and `cwd` are
both present and that `cwd` is the project directory. Record the observed
payload in the spec's §3.1 table (replacing "code-read") in the same commit.
If `cwd` is wrong or absent, fix the plugin before proceeding — the fallback
(`opencode -c`) still works, but the record would carry a wrong directory.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/opencode/plugin_template.go internal/agent/opencode/plugin_template_contract_test.go internal/agent/opencode/plugin_template_bun_integration_test.go
git commit -m "feat(opencode): plugin reports cwd on session start"
```

---

### Task 4: Stamp the tmux generation on session payloads

**Files:**
- Modify: `internal/module/session/provider.go:18-33` (`SessionInfo`)
- Modify: `internal/module/session/service.go` (populate the field)
- Modify: `internal/module/session/watcher.go:78`, `:213-217`
- Test: `internal/module/session/watcher_test.go`

**Interfaces:**
- Produces: `SessionInfo.TmuxInstance` (JSON `tmux_instance`) on every
  `/api/sessions` response and every `sessions` WS broadcast, plus
  `SessionProvider.TmuxInstance()`. **Task 5** stamps the envelope with it;
  Tasks 6 and 11 read the wire field.

**Context — why the hash must include it:** `hashSessions`
(`watcher.go:213-217`) hashes only the marshalled list. A tmux restart between
two ticks that recreates identical sessions produces an identical hash, so no
broadcast fires and the SPA keeps the previous generation forever (spec §4.6,
review R3 finding 1). Reading the instance per tick and hashing it with the
list makes any restart observable. `config.GetTmuxInstance()` returns `""` on
error/timeout — hash and broadcast that honestly; the next successful tick
changes the hash again, so it self-heals.

- [ ] **Step 1: Write the failing test**

```go
// internal/module/session/watcher_test.go — real fixture API:
//   newWatcherTestModule(t) → (*SessionModule, *tmux.FakeExecutor, *core.EventsBroadcaster)
//   fake.AddSession(name, cwd) · mod.tickNormal() · events.AddTestSubscriber()
// tickNormal broadcasts directly (watcher.go:84-88), bypassing the 500ms
// debounce that broadcastSessions applies, so back-to-back ticks are fine.

// `events.AddTestSubscriber()` returns *core.EventSubscriber
// (internal/core/events.go:139). Frames are the outer envelope, so parse it and
// keep only the sessions events before asserting on the inner JSON.
func drainSessions(t *testing.T, sub *core.EventSubscriber) []string {
	t.Helper()
	var out []string
	timeout := time.After(100 * time.Millisecond)
	for {
		select {
		case msg := <-sub.SendCh():
			var env struct {
				Type  string `json:"type"`
				Value string `json:"value"`
			}
			if err := json.Unmarshal(msg, &env); err != nil || env.Type != "sessions" {
				continue
			}
			out = append(out, env.Value)
		case <-timeout:
			return out
		}
	}
}

func TestTickNormal_TmuxRestartWithIdenticalList_Broadcasts(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	sub := events.AddTestSubscriber()
	defer events.RemoveTestSubscriber(sub)

	fake.AddSession("dev", "/w")
	mod.tmuxInstanceFn = func() string { return "111:1000" }
	mod.tickNormal()
	require.Len(t, drainSessions(t, sub), 1, "first tick must broadcast")

	// Same session list, new tmux server.
	mod.tmuxInstanceFn = func() string { return "222:2000" }
	mod.tickNormal()
	got := drainSessions(t, sub)
	require.Len(t, got, 1, "restart with an identical list must still broadcast")
	assert.Contains(t, got[0], `"tmux_instance":"222:2000"`)
}

func TestTickNormal_UnchangedInstanceAndList_DoesNotBroadcast(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	sub := events.AddTestSubscriber()
	defer events.RemoveTestSubscriber(sub)

	fake.AddSession("dev", "/w")
	mod.tmuxInstanceFn = func() string { return "111:1000" }
	mod.tickNormal()
	drainSessions(t, sub)

	mod.tickNormal()
	assert.Empty(t, drainSessions(t, sub), "unchanged state must not broadcast")
}

func TestListSessions_SamplesInstanceOutsideTheTick(t *testing.T) {
	// A restart between two ticks must not be reported with the previous
	// generation by the list path.
	mod, fake, _ := newWatcherTestModule(t)
	fake.AddSession("dev", "/w")
	mod.tmuxInstanceFn = func() string { return "111:1000" }
	mod.tickNormal()

	mod.tmuxInstanceFn = func() string { return "222:2000" }
	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	require.NotEmpty(t, sessions)
	assert.Equal(t, "222:2000", sessions[0].TmuxInstance,
		"list must sample the instance, not reuse the last tick's value")
}

func TestSessionInfo_TmuxInstanceKeyAlwaysPresent(t *testing.T) {
	raw, err := json.Marshal(SessionInfo{Code: "abc", Name: "dev"})
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"tmux_instance":""`,
		"the key must be transmitted even when unknown (spec §4.6)")
}

func TestTickNormal_InstanceProbeFailure_PropagatesEmpty(t *testing.T) {
	mod, fake, _ := newWatcherTestModule(t)
	fake.AddSession("dev", "/w")
	mod.tmuxInstanceFn = func() string { return "" }
	mod.tickNormal()

	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	require.NotEmpty(t, sessions)
	assert.Equal(t, "", sessions[0].TmuxInstance, "a probe failure must propagate empty, not a stale value")
}
```

`core.TestSubscriber` is what `events.AddTestSubscriber()` already returns
(see `TestBroadcastSessionsDebounce`, `watcher_test.go:106-135`); check its
exact exported type name there and match it.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/module/session/ -run TestTickNormal_Tmux -v`
Expected: FAIL — `tmuxInstanceFn` undefined, `TmuxInstance` undefined.

- [ ] **Step 3: Implement**

Add to `SessionInfo` (live section, next to `CurrentCommand`):

```go
	// TmuxInstance is the tmux server identity ("<pid>:<start_time>") the
	// session belongs to. It changes on every tmux server restart, which is
	// what lets a client tell a genuinely-live session from a reused session
	// code after a reboot (session codes are a reversible encoding of $N, so
	// $0 mints the same code every boot). Empty means "unknown" — never treat
	// two empties as a match.
	//
	// Deliberately NOT omitempty: the SPA distinguishes "unknown" from
	// "absent field / old daemon", and spec §4.6 requires "" to be
	// transmitted rather than elided.
	TmuxInstance string `json:"tmux_instance"`
```

Extend the `SessionProvider` interface (`internal/module/session/provider.go:6-11`)
with

```go
	// TmuxInstance returns the current tmux server identity, or "" when it
	// cannot be determined. Consumed by the agent module, which already holds
	// this provider (internal/module/agent/module.go:33,196-201), to stamp the
	// generation onto the provenance envelope in Task 5.
	TmuxInstance() string
```

so Task 5 has a real source. **Every implementer must gain the method in this
same commit** or the build breaks:

| Implementer | Location |
|---|---|
| `session.SessionModule` (real) | `internal/module/session/module.go:60` |
| agent `fakeSessionProvider` | `internal/module/agent/fakes_test.go:18` |
| agent `fakeFastSessionProvider` | `internal/module/agent/fast_path_test.go:21` |
| fs `fakeSessionProvider` | `internal/module/fs/module_test.go:16` |
| stream `fakeSessionProvider` | `internal/module/stream/handler_test.go:21` |

The monitor module declares its own narrow interface
(`internal/module/monitor/module.go:31`) and is unaffected. The fakes can
return a fixed string; the agent fakes need a settable field so Task 5's tests
can assert the stamped value.

Add the seam on the module (`module.go`), defaulting to the real reader:

```go
	tmuxInstanceFn func() string // swapped in tests
```

initialised to `config.GetTmuxInstance`.

**Sample at every boundary, not only on the tick.** A tick-scoped cache would
hand a stale generation to any payload produced between a restart and the next
tick — the HTTP list/get handlers, the snapshot pushed immediately on subscribe
(`internal/module/session/module.go:97`) and the debounced `broadcastSessions`
path all qualify. Implement the interface method as the single sampling point:

```go
// TmuxInstance re-reads the tmux server identity. Every payload that carries a
// generation samples it here rather than reusing a tick-scoped value, so a
// restart between ticks cannot be labelled with the previous generation.
// Returns "" when the probe fails; "" is a legitimate, transmitted value
// meaning "unknown" — never a match for another "".
func (m *SessionModule) TmuxInstance() string { return m.tmuxInstanceFn() }
```

Populate `info.TmuxInstance` from it wherever `SessionInfo` is built in
`service.go` (both the list path and the single-get path), and change the
hash:

```go
func hashSessions(tmuxInstance string, sessions []SessionInfo) string {
	data, _ := json.Marshal(struct {
		Instance string        `json:"i"`
		Sessions []SessionInfo `json:"s"`
	}{tmuxInstance, sessions})
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h[:8])
}
```

updating the caller at `watcher.go:78`.

⚠️ `hashSessions` has a second caller in the tests:
`TestHashSessionsChangesWhenPaneTitleChanges` (`watcher_test.go:300`) calls it
with one argument. **Update that call to pass an instance** (any constant, e.g.
`"i"`, for both sides — the test is about pane titles). This is the one place
in the plan where editing an existing test is correct and expected; the
"never edit an existing test" rule in Global Constraints is scoped to Phase 1's
pure refactor.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/module/session/`
Expected: PASS

- [ ] **Step 5: Full suite + commit**

```bash
go test ./...
git add internal/module/session/ internal/module/agent/fakes_test.go internal/module/agent/fast_path_test.go internal/module/fs/module_test.go internal/module/stream/handler_test.go
git commit -m "feat(session): stamp the tmux generation on session payloads"
```

---

### Task 5: Emit the provenance envelope, gated on the mutation outcome

**Files:**
- Create: `internal/module/agent/provenance.go`
- Create: `internal/module/agent/provenance_test.go`
- Modify: `internal/module/agent/frame_ops.go` (`applyFrameEvent`, the return
  paths at ~542-600, ~828-845 and ~860-880)

**Interfaces:**
- Consumes: `classifyAncestor` (Task 1), `Detail["session_id"]` /
  `Detail["cwd"]` (Task 2), `FrameTraceMeta` (`frame_ops.go:54-73`).
- Produces: `NormalizedEvent.Detail["pdx_provenance"]`, shape:
  ```go
  type Provenance struct {
      OwnerSessionStart bool   `json:"owner_session_start"`
      AgentType         string `json:"agent_type"`
      SessionID         string `json:"session_id,omitempty"`
      Cwd               string `json:"cwd,omitempty"`
      TmuxPaneID        string `json:"tmux_pane_id"`
      TmuxInstance      string `json:"tmux_instance"`
  }
  ```
  Task 11 (SPA) reads it.

**Context — the ordering trap:** the pre-Upsert verdict is **not** final.
`applyFrameEvent` runs a second proxy attempt after the Upsert
(`reconcileCreatedFrameAsProxy`, `frame_ops.go:828-845`), which folds a frame
the pre-walk judged root — `TestPhase35_IT3_PreWalkMiss_PostReconcileHit`
(`frame_ops_test.go:2535`) pins that exact sequence. The envelope must
therefore be attached on the **outcome**, at the point where
`FrameTraceMeta` is finalized, not where the verdict is computed.

The four conditions (spec §4.3.1): pre-walk verdict `VerdictRoot`; not taken
by the pre-Upsert fast path; `canonicalized == false`; resulting frame is the
sender's own with `ParentFrameID == ""`. Plus `req.SenderUncertain == false`.

- [ ] **Step 1: Write the failing test**

```go
// internal/module/agent/provenance_test.go
package agent

import "testing"

func TestProvenance_RootSessionStart_EmitsEnvelope(t *testing.T) {
	m := newProxyTestModule(t)
	req := EventRequest{
		TmuxPaneID: "%5", AgentType: "codex", SenderPID: 200,
		SenderStartTime: "t200", PurdexName: "PdxSessionStart",
		RawEvent: []byte(`{"session_id":"S1","cwd":"/w/p"}`),
	}
	withProcessTree(t, map[int]int{200: 999})
	m.sessions = fakeProviderWithInstance("4471:1788740000")

	ev := m.buildNormalizedForTest(req)
	p := ev.Detail["pdx_provenance"].(Provenance)
	if !p.OwnerSessionStart || p.AgentType != "codex" || p.SessionID != "S1" ||
		p.Cwd != "/w/p" || p.TmuxPaneID != "%5" || p.TmuxInstance != "4471:1788740000" {
		t.Fatalf("envelope = %+v", p)
	}
}

func TestProvenance_ProxyCollapsed_NoEnvelope_OuterTypeIsOwner(t *testing.T) {
	// codex started inside a live cc pane: the broadcast must say cc and must
	// NOT carry codex's session id. Spec §4.3.1.
	m := newProxyTestModule(t)
	seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	withProcessTree(t, map[int]int{200: 100})
	withLivePids(t, map[int]string{100: "t100"})
	req := EventRequest{
		TmuxPaneID: "%5", AgentType: "codex", SenderPID: 200,
		SenderStartTime: "t200", PurdexName: "PdxSessionStart",
		RawEvent: []byte(`{"session_id":"S1","cwd":"/w/p"}`),
	}

	ev := m.buildNormalizedForTest(req)
	if _, ok := ev.Detail["pdx_provenance"]; ok {
		t.Fatalf("proxy-collapsed event must not carry provenance")
	}
	if ev.AgentType != "cc" {
		t.Fatalf("outer agent_type = %q, want cc", ev.AgentType)
	}
}

func TestProvenance_SameTypeAbove_NoEnvelope(t *testing.T) {
	m := newProxyTestModule(t)
	seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	withProcessTree(t, map[int]int{200: 100})
	withLivePids(t, map[int]string{100: "t100"})
	req := EventRequest{
		TmuxPaneID: "%5", AgentType: "cc", SenderPID: 200,
		SenderStartTime: "t200", PurdexName: "PdxSessionStart",
		RawEvent: []byte(`{"session_id":"S2"}`),
	}

	if _, ok := m.buildNormalizedForTest(req).Detail["pdx_provenance"]; ok {
		t.Fatalf("cc-inside-cc must not carry provenance")
	}
}

func TestProvenance_PostUpsertReconcile_NoEnvelope(t *testing.T) {
	// Pre-walk says root, reconcileCreatedFrameAsProxy folds it afterwards.
	// Mirrors TestPhase35_IT3_PreWalkMiss_PostReconcileHit's fixture.
	m := newProxyTestModule(t)
	seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	withProcessTreeSequence(t, 200, []int{999, 999, 100}) // miss, miss, hit
	req := EventRequest{
		TmuxPaneID: "%5", AgentType: "codex", SenderPID: 200,
		SenderStartTime: "t200", PurdexName: "PdxSessionStart",
		RawEvent: []byte(`{"session_id":"S3"}`),
	}

	if _, ok := m.buildNormalizedForTest(req).Detail["pdx_provenance"]; ok {
		t.Fatalf("post-Upsert reconcile must revoke the envelope")
	}
}

func TestProvenance_SenderUncertain_NoEnvelope(t *testing.T) {
	m := newProxyTestModule(t)
	withProcessTree(t, map[int]int{200: 999})
	req := EventRequest{
		TmuxPaneID: "%5", AgentType: "codex", SenderPID: 200,
		SenderStartTime: "t200", PurdexName: "PdxSessionStart",
		SenderUncertain: true, RawEvent: []byte(`{"session_id":"S4"}`),
	}

	if _, ok := m.buildNormalizedForTest(req).Detail["pdx_provenance"]; ok {
		t.Fatalf("uncertain sender must not carry provenance")
	}
}
```

`buildNormalizedForTest` is a thin test-only seam added in Step 3 that calls
the **production** `applyFrameEvent` + `buildProjectionNormalized` pair and
returns the resulting `agentpkg.NormalizedEvent` — it must not re-implement the
attachment condition, or the tests would assert the test helper back to
itself. `withProcessTreeSequence` does **not** exist yet — Task 1 deliberately
wrote only the three helpers it used, so write this one here, alongside them in
`ancestor_test.go`: it swaps `readProcessInfoFn` for one that returns the given
PPIDs in call order (call 1 and 2 miss, call 3 hits, mirroring
`TestPhase35_IT3_PreWalkMiss_PostReconcileHit`'s fixture).

The proxy and post-reconcile cases additionally need the liveness fixtures
(`withLivePids`) that Task 1 introduced, or the candidate is treated as stale
and the walk continues past it. In the post-reconcile case, assert **first**
that the frame really was canonicalized (the parent row gained a
`proxy:codex:…` ref) and only then that no envelope was emitted — otherwise a
passing test could merely mean the fixture never reached the reconcile.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/module/agent/ -run TestProvenance -v`
Expected: FAIL — `Provenance` undefined.

- [ ] **Step 3: Implement**

`internal/module/agent/provenance.go`:

```go
package agent

import agentpkg "github.com/wake/purdex/internal/agent"

// Provenance is the self-contained rebuild-record envelope. It is deliberately
// separate from NormalizedEvent.AgentType: on a proxy-collapsed event the
// outer type names the session projection winner, which may be a different
// agent in a different tmux pane (frame_ops.go:1129, :1170-1182). Consumers of
// the rebuild record read ONLY this struct. See spec §4.3.1.
type Provenance struct {
	OwnerSessionStart bool   `json:"owner_session_start"`
	AgentType         string `json:"agent_type"`
	SessionID         string `json:"session_id,omitempty"`
	Cwd               string `json:"cwd,omitempty"`
	TmuxPaneID        string `json:"tmux_pane_id"`
	TmuxInstance      string `json:"tmux_instance"`
}

func buildProvenance(req EventRequest, result agentpkg.DeriveResult, tmuxInstance string) Provenance {
	return Provenance{
		OwnerSessionStart: true,
		AgentType:         req.AgentType,
		SessionID:         strFromDetail(result.Detail, "session_id"),
		Cwd:               strFromDetail(result.Detail, "cwd"),
		TmuxPaneID:        req.TmuxPaneID,
		TmuxInstance:      tmuxInstance,
	}
}
```

**Do not thread two booleans through `applyFrameEvent`.** That function has
20+ `return nil, FrameTraceMeta{}, err` sites and 10+ populated
`return projection, FrameTraceMeta{...}` sites; touching every literal is both
a large diff and easy to get wrong. Instead add **one nullable field**:

```go
// FrameTraceMeta (frame_ops.go:54-73) gains:

	// Provenance is non-nil only when this event was a SessionStart that
	// ended up owning its own top-level frame — i.e. it survived both the
	// pre-Upsert proxy fast-path and the post-Upsert reconcile. Every other
	// return path leaves it nil by zero value, which is the fail-safe: no
	// field set, no envelope emitted.
	Provenance *Provenance
```

Every existing construction site keeps compiling untouched and yields `nil`.
Set it in exactly one place — the `created_frame` / `updated_frame` return at
`frame_ops.go:860-880`, after `reason` and `decision` are computed:

```go
	var prov *Provenance
	if lifecycle == agentpkg.LifecycleSessionStart && !req.SenderUncertain &&
		verdict == VerdictRoot && stored.ParentFrameID == "" {
		p := buildProvenance(req, result, m.sessionTmuxInstance())
		prov = &p
	}
```

where `verdict` is the `classifyAncestor` result computed once at the top of
the `LifecycleSessionStart` handling — replace the existing `findProxyParent`
call on that path with it so the walk still runs exactly once.

The two proxy paths return **before** reaching this site: the pre-Upsert
fast-path returns at `frame_ops.go:572-600` and the post-Upsert reconcile
returns at `:838-850`. Neither sets `Provenance`, which is precisely how a
post-Upsert canonicalization revokes the envelope — no explicit `canonicalized`
flag is needed.

In the handler, at `handler.go:561` — where `buildProjectionNormalized` is
actually called; `:415-460` is before the normalized event exists:

```go
	if frameMeta.Provenance != nil {
		if normalized.Detail == nil {
			normalized.Detail = map[string]any{}
		}
		normalized.Detail["pdx_provenance"] = *frameMeta.Provenance
	}
```

The generation comes from `m.sessions.TmuxInstance()` — the `SessionProvider`
the agent module already holds (`internal/module/agent/module.go:33,196-201`),
extended by Task 4, which is why Task 4 runs first. Guard for a nil provider
(`m.sessions == nil` in some test setups) by treating it as `""`, which
`parseProvenance` (Task 11) rejects, so a half-wired daemon writes no record.

`ownedBySender` from the snippet above is therefore not needed as a separate
function; delete it from `provenance.go` and keep only `Provenance` and
`buildProvenance`.

**Return-path audit — confirm each of these before committing:**

| `applyFrameEvent` return | Provenance | Why |
|---|---|---|
| `frame_ops.go:77,83` skipped (`frame_store_unavailable`, `derive_invalid`) | nil (zero value) | nothing was recorded |
| any `return nil, FrameTraceMeta{}, err` (20+ sites) | nil (zero value) | errors never grant provenance |
| `:572-600` pre-Upsert proxy fast-path (`proxy_subagent_attached`) | nil — returns before the set site | cross-type nesting |
| `:838-850` post-Upsert `reconcileCreatedFrameAsProxy` hit | nil — returns before the set site | this is what revokes a pre-walk `VerdictRoot` |
| `:860-880` `created_frame` / `updated_frame` | set iff `VerdictRoot && ParentFrameID == "" && !SenderUncertain` | the only owning outcome |

A `SessionStart` that **updates an existing frame** (`decision:
updated_frame`, e.g. a `/clear` in a session that already has a frame) reaches
the same `:860-880` return and must be granted provenance — that is how a new
session id replaces the old record. Do not restrict the set site to
`decision == "created_frame"`.

The verdict variable must be in scope at `:860-880`; declare it in the
`LifecycleSessionStart` branch and default it to `VerdictIndeterminate` for
every other lifecycle so a non-SessionStart can never satisfy the condition
(`VerdictRoot` is the zero value of the type, which is exactly why the
`lifecycle == LifecycleSessionStart` clause is load-bearing).

- [ ] **Step 4: Run tests**

Run: `go test ./internal/module/agent/ -v`
Expected: PASS — including `TestPhase35_IT3_PreWalkMiss_PostReconcileHit`
unchanged.

- [ ] **Step 5: Commit**

```bash
git add internal/module/agent/provenance.go internal/module/agent/provenance_test.go internal/module/agent/frame_ops.go internal/module/agent/handler.go
git commit -m "feat(agent): emit provenance envelope on owner session starts"
```

---

# Phase 3 — Generation awareness (SPA)

### Task 6: Record and compare the pane's tmux generation

**Files:**
- Modify: `spa/src/lib/host-api.ts` (`Session` interface)
- Modify: `spa/src/stores/useTabStore.ts` (add `markTerminatedForGeneration`)
- Modify: `spa/src/hooks/useMultiHostEventWs.ts:100-140`
- Test: `spa/src/stores/useTabStore.generation.test.ts`,
  `spa/src/hooks/useMultiHostEventWs.generation.test.ts`

**Interfaces:**
- Consumes: `Session.tmux_instance` (Task 5).
- Produces:
  ```ts
  markTerminatedForGeneration(
    hostId: string, sessionCode: string,
    expectedTmuxInstance: string, reason: TerminatedReason,
  ): void
  ```
  Task 16 uses the same field for grouping.

**Context:** today `useMultiHostEventWs.ts:111-124` marks a pane terminated
only when its code is missing from the live list, and it refreshes
`cachedName` *before* deciding (line 106). Because codes are reused across tmux
restarts, a pane can find its code alive and attach to a stranger. The fix
compares generations, and decides before updating anything. Panes whose
recorded instance is `''` keep today's behaviour exactly.

- [ ] **Step 1: Write the failing test**

```ts
// spa/src/stores/useTabStore.generation.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { useTabStore } from './useTabStore'
import { createTab } from '../types/tab'

function seedPane(tmuxInstance: string, sessionCode = 'abc123') {
  const tab = createTab({
    kind: 'tmux-session', hostId: 'h1', sessionCode,
    mode: 'terminal', cachedName: 'dev', tmuxInstance,
  })
  useTabStore.setState({ tabs: { [tab.id]: tab }, tabOrder: [tab.id], activeTabId: tab.id })
  return tab
}

describe('markTerminatedForGeneration', () => {
  beforeEach(() => useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null }))

  it('marks a pane whose recorded generation differs', () => {
    const tab = seedPane('111:1000')
    useTabStore.getState().markTerminatedForGeneration('h1', 'abc123', '111:1000', 'tmux-restarted')
    const pane = useTabStore.getState().tabs[tab.id].layout
    expect(pane.type === 'leaf' && pane.pane.content.kind === 'tmux-session'
      && pane.pane.content.terminated).toBe('tmux-restarted')
  })

  it('leaves a sibling pane already bound to the new generation alone', () => {
    const stale = seedPane('111:1000')
    const fresh = createTab({
      kind: 'tmux-session', hostId: 'h1', sessionCode: 'abc123',
      mode: 'terminal', cachedName: 'dev', tmuxInstance: '222:2000',
    })
    useTabStore.setState((s) => ({ tabs: { ...s.tabs, [fresh.id]: fresh }, tabOrder: [...s.tabOrder, fresh.id] }))

    useTabStore.getState().markTerminatedForGeneration('h1', 'abc123', '111:1000', 'tmux-restarted')

    const freshLayout = useTabStore.getState().tabs[fresh.id].layout
    expect(freshLayout.type === 'leaf' && freshLayout.pane.content.kind === 'tmux-session'
      && freshLayout.pane.content.terminated).toBeUndefined()
    const staleLayout = useTabStore.getState().tabs[stale.id].layout
    expect(staleLayout.type === 'leaf' && staleLayout.pane.content.kind === 'tmux-session'
      && staleLayout.pane.content.terminated).toBe('tmux-restarted')
  })
})
```

```ts
// spa/src/hooks/useMultiHostEventWs.generation.test.ts — behaviour of the
// sessions handler, exercised through the extracted pure function.
import { describe, it, expect } from 'vitest'
import { reconcileSessionsPayload } from '../lib/rebuild/reconcile'

const pane = (tmuxInstance: string) => ({
  hostId: 'h1', sessionCode: 'abc123', tmuxInstance,
})

describe('reconcileSessionsPayload', () => {
  it('marks tmux-restarted even when the code is present in the live list', () => {
    const out = reconcileSessionsPayload({
      hostId: 'h1',
      sessions: [{ code: 'abc123', name: 'dev', tmux_instance: '222:2000' }],
      panes: [pane('111:1000')],
    })
    expect(out.terminate).toEqual([
      { hostId: 'h1', sessionCode: 'abc123', expectedTmuxInstance: '111:1000', reason: 'tmux-restarted' },
    ])
  })

  it('marks nothing when either side is unknown', () => {
    for (const [recorded, live] of [['', '222:2000'], ['111:1000', ''], ['', '']]) {
      const out = reconcileSessionsPayload({
        hostId: 'h1',
        sessions: [{ code: 'abc123', name: 'dev', tmux_instance: live }],
        panes: [pane(recorded)],
      })
      expect(out.terminate).toEqual([])
    }
  })

  it('still marks a code missing from the live list as session-closed', () => {
    const out = reconcileSessionsPayload({ hostId: 'h1', sessions: [], panes: [pane('111:1000')] })
    expect(out.terminate[0].reason).toBe('session-closed')
  })

  it('adopts the live generation onto panes that have none', () => {
    const out = reconcileSessionsPayload({
      hostId: 'h1',
      sessions: [{ code: 'abc123', name: 'dev', tmux_instance: '222:2000' }],
      panes: [pane('')],
    })
    expect(out.adoptInstance).toEqual([{ sessionCode: 'abc123', tmuxInstance: '222:2000' }])
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `pnpm --prefix spa exec vitest run generation`
Expected: FAIL — `markTerminatedForGeneration` and
`reconcileSessionsPayload` do not exist.

- [ ] **Step 3: Implement**

Add `tmux_instance?: string` to the `Session` interface in
`spa/src/lib/host-api.ts:6-17`.

**Stamp the generation where panes are born, not only where they are
reconciled.** Every construction site currently hard-codes `tmuxInstance: ''`,
and `SessionPickerList.tsx:51` reads the ambient `runtime[hostId]?.info`, which
nothing populates (spec §3.5). A pane opened after a stable session list has
landed would otherwise never receive a generation and never build a record.
Change all of them to take the value from the **selected `Session` payload**:

| Site | Line |
|---|---|
| `spa/src/components/SessionSection.tsx` | 29, 232 |
| `spa/src/components/hosts/SessionsSection.tsx` | 152, 196 |
| `spa/src/features/workspace/components/WorkspaceQuickActionsPopover.tsx` | 180 |
| `spa/src/features/workspace/components/WorkspaceQuickCommandsContextMenu.tsx` | 137 |
| `spa/src/hooks/useNotificationDispatcher.ts` | 365 |
| `spa/src/components/SessionPickerList.tsx` | 51 (stop reading `runtime.info`) |
| `spa/src/components/TerminatedPane.tsx` | 26-34 (`handleSelect`) |

Sites that genuinely have no `Session` in hand keep `''`, which the empty-
instance rules treat as unknown — never as a match.

Add a test per shape:

```ts
it('opens a pane carrying the selected session\'s generation', () => {
  const onSelect = vi.fn()
  renderSessionSection({ sessions: [
    { code: 'abc123', name: 'dev', cwd: '/w', mode: 'terminal',
      cc_session_id: '', cc_model: '', has_relay: false, tmux_instance: '222:2000' },
  ], onSelect })
  fireEvent.click(screen.getByText('dev'))
  expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ tmuxInstance: '222:2000' }))
})
```

Create `spa/src/lib/rebuild/reconcile.ts` with the pure decision function
returning `{ terminate: [...], adoptInstance: [...] }`, applying exactly the
rules asserted above. Add `markTerminatedForGeneration` to `useTabStore` next
to `markTerminated`, reusing the same layout-walk helper but adding the
instance predicate (an empty recorded instance matches the old rule).

Rewrite the `sessions` branch of `useMultiHostEventWs.ts` to call
`reconcileSessionsPayload` **before** `updateSessionCache`, then apply its two
outputs.

- [ ] **Step 4: Run tests**

Run: `pnpm --prefix spa exec vitest run`
Expected: PASS (full suite)

- [ ] **Step 5: Commit**

```bash
pnpm --prefix spa run lint
git add spa/src/lib/host-api.ts spa/src/lib/rebuild/reconcile.ts spa/src/stores/useTabStore.ts spa/src/hooks/useMultiHostEventWs.ts spa/src/components/SessionSection.tsx spa/src/components/hosts/SessionsSection.tsx spa/src/components/SessionPickerList.tsx spa/src/components/TerminatedPane.tsx spa/src/features/workspace/components/WorkspaceQuickActionsPopover.tsx spa/src/features/workspace/components/WorkspaceQuickCommandsContextMenu.tsx spa/src/hooks/useNotificationDispatcher.ts spa/src/stores/useTabStore.generation.test.ts spa/src/hooks/useMultiHostEventWs.generation.test.ts
git commit -m "feat(spa): detect tmux restarts by generation, not code absence"
```

---

### Task 7: Gate terminal attach on the current connection epoch

**Files:**
- Modify: `spa/src/stores/useHostStore.ts` (`HostRuntime` gains `attachReady`)
- Modify: `spa/src/lib/host-events.ts` (socket epoch + ticket-await re-check)
- Modify: `spa/src/lib/ws.ts:20-32` (gate re-check after the ticket await)
- Modify: `spa/src/hooks/useMultiHostEventWs.ts` (open/close `attachReady`)
- Modify: `spa/src/hooks/useTerminalWs.ts:51-58` **and its `connectTerminal`
  call site**
- Test: `spa/src/hooks/useTerminalWs.gate.test.ts`

**Interfaces:**
- Consumes: nothing new.
- Produces: `runtime[hostId].attachReady: boolean`.

**Context — two traps, both verified:**

1. A boot-only gate is insufficient. Health recovery flips the host to
   `connected` (`useMultiHostEventWs.ts:82`) before host-events reconnects, so
   after an offline tmux restart a terminal can attach to a stranger before the
   fresh list lands (spec §4.6, R3 finding 2).
2. **`canReconnect` is not the initial connect.** `useTerminalWs.ts:51-58`
   builds `canReconnect`, which `ws.ts:44` consults only on the *retry* path;
   the first `connect()` runs unconditionally at `ws.ts:80`. Gating only
   `canReconnect` would let the very first attach through — exactly the case
   that matters after an app restart. The gate must sit on **both**:
   `useTerminalWs` must not call `connectTerminal` until
   `canAttachTerminal(hostId)` is true (re-running the effect when it flips),
   and `canReconnect` keeps it for retries.

**Epoch contract — filter inside the transport, not at the consumer.**
`reconnect` / `reconnectWithTicket` (`host-events.ts:80-94`) reuse the **same**
connection object and the **same** `onEvent` closure; they only null the old
socket's `onclose` and open a new one. So a closure-captured epoch on the
consumer side would reject the *new* socket's payloads too. Put the epoch
inside `connectHostEvents` instead, where each socket's handlers are created:

```ts
// spa/src/lib/host-events.ts
let socketEpoch = 0

async function connect() {
  // Claim an epoch BEFORE the await so a slower ticket cannot resurrect a
  // superseded attempt (reconnectWithTicket bumps it again immediately).
  const myEpoch = ++socketEpoch
  const ticket = pendingTicket ?? (getTicket ? await getTicket().catch(() => null) : null)
  pendingTicket = undefined
  if (closed || myEpoch !== socketEpoch) return   // ← the ticket-await re-check
  // …create the socket…
  ws.onmessage = (e) => {
    if (myEpoch !== socketEpoch) return           // ← stale socket's queued frames
    // …existing parse + onEvent(event)…
  }
}
```

With the filter here, a superseded socket's frames never reach `onEvent`, so
`useMultiHostEventWs` needs no epoch parameter and its signature is unchanged.
`runtime.sessionsEpoch` is then only a diagnostic and can be dropped from
`HostRuntime`.

Apply the same post-await re-check to the terminal transport: `ws.ts:20-32`
resolves its ticket and then checks only `closed` before `setupWs`. Add the
gate re-check immediately before the socket is created:

```ts
  if (closed) return
  if (canReconnect && !canReconnect()) { scheduleRetry(); return }   // gate closed while awaiting
  setupWs(wsUrl)
```

Sequence per host in `useMultiHostEventWs`:

- **starting a connection (initial or retry):** set `attachReady: false`;
- **socket closed / error:** set `attachReady: false` immediately (do not wait
  for the next open);
- **any `sessions` payload, after reconciliation:** set `attachReady: true` —
  stale payloads can no longer arrive, so no epoch comparison is needed here.

In `useTerminalWs`, subscribe to `runtime[hostId]?.attachReady` and skip the
`connectTerminal` call while it is false; also add
`&& canAttachTerminal(hostId)` to `canReconnect`.

Add two tests that exercise the real wiring rather than reading `attachReady`
back:

```ts
it('constructs no terminal socket until the gate opens', async () => {
  const ctor = vi.fn()
  vi.stubGlobal('WebSocket', class { constructor(url: string) { ctor(url) } close() {} } as never)
  useHostStore.setState({ runtime: { h1: { status: 'connected', attachReady: false } } })
  renderTerminalPane('h1', 'abc123')
  expect(ctor).not.toHaveBeenCalled()

  act(() => { useHostStore.getState().setRuntime('h1', { attachReady: true }) })
  await waitFor(() => expect(ctor).toHaveBeenCalledTimes(1))
})

// spa/src/lib/host-events.epoch.test.ts — transport-level, where the fix lives.
it('drops frames from a socket that reconnect superseded', () => {
  const onEvent = vi.fn()
  const sockets: FakeSocket[] = []
  vi.stubGlobal('WebSocket', class extends FakeSocket { constructor(u: string) { super(u); sockets.push(this) } })

  const conn = connectHostEvents('ws://h/events', onEvent)
  conn.reconnect()                       // second socket supersedes the first
  sockets[0].emit(JSON.stringify({ type: 'sessions', value: '[]' }))
  expect(onEvent).not.toHaveBeenCalled()

  sockets[1].emit(JSON.stringify({ type: 'sessions', value: '[]' }))
  expect(onEvent).toHaveBeenCalledTimes(1)
})

it('abandons a connect whose ticket resolved after a newer connect started', async () => {
  let releaseTicket!: (t: string) => void
  const getTicket = () => new Promise<string>((r) => { releaseTicket = r })
  const sockets: FakeSocket[] = []
  vi.stubGlobal('WebSocket', class extends FakeSocket { constructor(u: string) { super(u); sockets.push(this) } })

  const conn = connectHostEvents('ws://h/events', vi.fn(), { getTicket })
  conn.reconnectWithTicket('fresh')      // supersedes the pending first attempt
  releaseTicket('stale')
  await Promise.resolve()
  expect(sockets.filter((s) => s.url.includes('ticket=stale'))).toHaveLength(0)
})

// spa/src/hooks/useTerminalWs.gate.test.ts
it('does not attach when the gate closed while the ticket was in flight', async () => {
  const ctor = vi.fn()
  vi.stubGlobal('WebSocket', class { constructor(url: string) { ctor(url) } close() {} } as never)
  let releaseTicket!: (t: string) => void
  mockTicket(() => new Promise<string>((r) => { releaseTicket = r }))
  useHostStore.setState({ runtime: { h1: { status: 'connected', attachReady: true } } })
  renderTerminalPane('h1', 'abc123')

  act(() => { useHostStore.getState().setRuntime('h1', { attachReady: false }) })
  releaseTicket('tk')
  await Promise.resolve()
  expect(ctor).not.toHaveBeenCalled()
})
```

- [ ] **Step 4: Run tests**

Run: `pnpm --prefix spa exec vitest run`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
pnpm --prefix spa run lint
git add spa/src/lib/rebuild/attach-gate.ts spa/src/stores/useHostStore.ts spa/src/lib/host-events.ts spa/src/lib/host-events.epoch.test.ts spa/src/lib/ws.ts spa/src/hooks/useMultiHostEventWs.ts spa/src/hooks/useTerminalWs.ts spa/src/hooks/useTerminalWs.gate.test.ts
git commit -m "feat(spa): gate terminal attach on the current connection's session payload"
```

---

### Task 8: Snapshot restore stamps the current generation

**Files:**
- Modify: `spa/src/lib/snapshot/restore.ts:130-145`
- Test: `spa/src/lib/snapshot/restore.generation.test.ts`

**Interfaces:**
- Consumes: `Session.tmux_instance` (Task 5).
- Produces: nothing new; fixes existing behaviour.

**Context:** `restore.ts:139` re-points a pane by spreading the old content and
replacing only code and name, so the pane keeps the **old** `tmuxInstance` and
Task 6's reconciliation immediately marks the freshly restored pane dead.

- [ ] **Step 1: Write the failing test**

```ts
// spa/src/lib/snapshot/restore.generation.test.ts
import { describe, it, expect } from 'vitest'
import { remapLayoutSessions } from './restore'

describe('remapLayoutSessions generation stamping', () => {
  it('stamps the rebuilt session\'s tmux_instance onto the pane', () => {
    const layout = {
      type: 'leaf' as const,
      pane: { id: 'p1', content: {
        kind: 'tmux-session' as const, hostId: 'h1', sessionCode: 'old111',
        mode: 'terminal' as const, cachedName: 'dev',
        tmuxInstance: '111:1000', terminated: 'tmux-restarted' as const,
      } },
    }
    const remap = { h1: { old111: {
      status: 'rebuilt' as const, newCode: 'new222',
      session: { code: 'new222', name: 'dev', cwd: '/w', mode: 'terminal',
        cc_session_id: '', cc_model: '', has_relay: false, tmux_instance: '222:2000' },
    } } }

    const out = remapLayoutSessions(layout, remap, { onlyTerminated: true })
    const content = out.type === 'leaf' && out.pane.content
    expect(content && content.kind === 'tmux-session' && content.tmuxInstance).toBe('222:2000')
    expect(content && content.kind === 'tmux-session' && content.terminated).toBeUndefined()
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `pnpm --prefix spa exec vitest run restore.generation`
Expected: FAIL — `tmuxInstance` is still `'111:1000'`.

- [ ] **Step 3: Implement**

At `restore.ts:130-145`, add `tmuxInstance: entry.session.tmux_instance ?? content.tmuxInstance`
to the re-point spread.

- [ ] **Step 4: Run tests**

Run: `pnpm --prefix spa exec vitest run snapshot`
Expected: PASS — all 72 existing snapshot tests plus the new one.

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/snapshot/restore.ts spa/src/lib/snapshot/restore.generation.test.ts
git commit -m "fix(snapshot): stamp the new tmux generation when re-pointing panes"
```

---

# Phase 4 — The record (SPA)

### Task 9: `PaneRebuildRecord` and the store action

**Files:**
- Modify: `spa/src/types/tab.ts`
- Modify: `spa/src/stores/useTabStore.ts`
- Test: `spa/src/stores/useTabStore.rebuild.test.ts`

**Interfaces:**
- Produces:
  ```ts
  export interface PaneRebuildRecord {
    sessionName: string
    tmuxInstance: string
    cwd?: string
    cwdSource?: 'agent-session-start' | 'pane-probe'
    agent?: { type: string; sessionId?: string; tmuxPaneId?: string; updatedAt: number }
    resumeCommand?: string
    unverified?: boolean
    capturedAt: number
  }

  type RebuildPatch =
    | { kind: 'agent-group'; record: Omit<PaneRebuildRecord, 'sessionName'> }
    | { kind: 'field'; field: 'cwd' | 'resumeCommand' | 'sessionName'; value: string }
    | { kind: 'probe-cwd'; cwd: string }
    | { kind: 'unverified'; unverified: boolean }

  // Session-scoped: every pane bound to (hostId, sessionCode, generation).
  // Used by the agent-group write and the cwd probe, which describe the
  // SESSION and are therefore true of all its panes.
  setPaneRebuild(
    hostId: string, sessionCode: string,
    expectedTmuxInstance: string, patch: RebuildPatch,
  ): void

  // Pane-scoped: exactly one pane. Used by every user edit, so editing one
  // pane's cwd does not rewrite its split sibling's record (spec §4.10 gives
  // each pane its own block, §4.11 resolves conflicting per-pane edits — both
  // require the edits to be able to differ).
  setPaneRebuildForPane(
    tabId: string, paneId: string,
    expected: { hostId: string; sessionCode: string; tmuxInstance: string },
    patch: Extract<RebuildPatch, { kind: 'field' }>,
  ): void
  ```
  Task 11 calls `setPaneRebuild`; Tasks 13/15/16 call
  `setPaneRebuildForPane`. Both stamp `capturedAt: Date.now()` on write, which
  is what makes Task 16's "latest edit wins" resolution meaningful.

**Context — the writer ranking (spec §4.1):** an agent-group write replaces
`agent`, `cwd`, `cwdSource`, `resumeCommand` and `capturedAt` **as one unit**
(a payload without `cwd` clears `cwd` rather than leaving the previous agent's
value beside a new session id); a field edit touches only that field; a
probe-cwd write applies only when `cwd` is unset.

- [ ] **Step 1: Write the failing test**

```ts
// spa/src/stores/useTabStore.rebuild.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { useTabStore } from './useTabStore'
import { createTab } from '../types/tab'

function seed(tmuxInstance = '111:1000') {
  const tab = createTab({
    kind: 'tmux-session', hostId: 'h1', sessionCode: 'abc123',
    mode: 'terminal', cachedName: 'dev', tmuxInstance,
  })
  useTabStore.setState({ tabs: { [tab.id]: tab }, tabOrder: [tab.id], activeTabId: tab.id })
  return tab
}
const rec = (tabId: string) => {
  const l = useTabStore.getState().tabs[tabId].layout
  return l.type === 'leaf' && l.pane.content.kind === 'tmux-session' ? l.pane.content.rebuild : undefined
}

describe('setPaneRebuild', () => {
  beforeEach(() => useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null }))

  it('writes the agent group as a unit', () => {
    const tab = seed()
    useTabStore.getState().setPaneRebuild('h1', 'abc123', '111:1000', {
      kind: 'agent-group',
      record: { tmuxInstance: '111:1000', cwd: '/w/p', cwdSource: 'agent-session-start',
        agent: { type: 'codex', sessionId: 'S1', tmuxPaneId: '%2', updatedAt: 5 },
        resumeCommand: 'codex resume S1', capturedAt: 5 },
    })
    expect(rec(tab.id)?.agent?.sessionId).toBe('S1')
    expect(rec(tab.id)?.cwd).toBe('/w/p')
  })

  it('clears cwd when a later agent group has none', () => {
    const tab = seed()
    const store = useTabStore.getState()
    store.setPaneRebuild('h1', 'abc123', '111:1000', {
      kind: 'agent-group',
      record: { tmuxInstance: '111:1000', cwd: '/w/p', cwdSource: 'agent-session-start',
        agent: { type: 'codex', sessionId: 'S1', updatedAt: 5 }, resumeCommand: 'codex resume S1', capturedAt: 5 },
    })
    store.setPaneRebuild('h1', 'abc123', '111:1000', {
      kind: 'agent-group',
      record: { tmuxInstance: '111:1000', agent: { type: 'codex', sessionId: 'S2', updatedAt: 6 },
        resumeCommand: 'codex resume S2', capturedAt: 6 },
    })
    expect(rec(tab.id)?.agent?.sessionId).toBe('S2')
    expect(rec(tab.id)?.cwd).toBeUndefined()
  })

  it('ignores a write for a different generation', () => {
    const tab = seed('111:1000')
    useTabStore.getState().setPaneRebuild('h1', 'abc123', '222:2000', {
      kind: 'field', field: 'cwd', value: '/other',
    })
    expect(rec(tab.id)?.cwd).toBeUndefined()
  })

  it('probe cwd fills only when unset and never overwrites an agent cwd', () => {
    const tab = seed()
    const store = useTabStore.getState()
    store.setPaneRebuild('h1', 'abc123', '111:1000', { kind: 'probe-cwd', cwd: '/probe' })
    expect(rec(tab.id)?.cwd).toBe('/probe')
    expect(rec(tab.id)?.cwdSource).toBe('pane-probe')

    store.setPaneRebuild('h1', 'abc123', '111:1000', {
      kind: 'agent-group',
      record: { tmuxInstance: '111:1000', cwd: '/agent', cwdSource: 'agent-session-start',
        agent: { type: 'cc', updatedAt: 7 }, resumeCommand: 'claude -c', capturedAt: 7 },
    })
    store.setPaneRebuild('h1', 'abc123', '111:1000', { kind: 'probe-cwd', cwd: '/probe2' })
    expect(rec(tab.id)?.cwd).toBe('/agent')
  })

  it('a per-pane edit does not touch a split sibling on the same session', () => {
    const tab = createTab({ kind: 'tmux-session', hostId: 'h1', sessionCode: 'abc123',
      mode: 'terminal', cachedName: 'dev', tmuxInstance: '111:1000' })
    const split = splitTabWithSecondPane(tab, 'p2')   // same (host, code, generation)
    useTabStore.setState({ tabs: { [split.id]: split }, tabOrder: [split.id], activeTabId: split.id })

    useTabStore.getState().setPaneRebuildForPane(split.id, 'p2',
      { hostId: 'h1', sessionCode: 'abc123', tmuxInstance: '111:1000' },
      { kind: 'field', field: 'cwd', value: '/only-p2' })

    expect(recordOfPane(split.id, 'p2')?.cwd).toBe('/only-p2')
    expect(recordOfPane(split.id, 'p1')?.cwd).toBeUndefined()
  })

  it('a field edit touches only that field', () => {
    const tab = seed()
    const store = useTabStore.getState()
    store.setPaneRebuild('h1', 'abc123', '111:1000', {
      kind: 'agent-group',
      record: { tmuxInstance: '111:1000', cwd: '/w/p', cwdSource: 'agent-session-start',
        agent: { type: 'cc', sessionId: 'S1', updatedAt: 5 }, resumeCommand: 'claude --resume S1', capturedAt: 5 },
    })
    store.setPaneRebuild('h1', 'abc123', '111:1000', { kind: 'field', field: 'cwd', value: '/edited' })
    expect(rec(tab.id)?.cwd).toBe('/edited')
    expect(rec(tab.id)?.resumeCommand).toBe('claude --resume S1')
    expect(rec(tab.id)?.agent?.sessionId).toBe('S1')
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `pnpm --prefix spa exec vitest run useTabStore.rebuild`
Expected: FAIL — `setPaneRebuild` is not a function.

- [ ] **Step 3: Implement**

Add `PaneRebuildRecord` and `rebuild?: PaneRebuildRecord` to
`spa/src/types/tab.ts`. Implement `setPaneRebuild` in `useTabStore` as a
functional `set` that walks every tab layout, matches
`(hostId, sessionCode, tmuxInstance)` — treating a pane whose recorded
instance is `''` as matching any expected instance — and applies the patch by
kind. `sessionName` defaults to the pane's `cachedName` when the record is
created.

Also rework the existing `updateSessionCache` (`useTabStore.ts:408-424`),
which today has two defects for this feature: it matches on `(hostId,
sessionCode)` only, and it inspects **only the primary pane**
(`getPrimaryPane(tab.layout)`), so a split tab's second terminal never gets its
name refreshed. New signature and behaviour:

```ts
updateSessionCache(
  hostId: string, sessionCode: string, cachedName: string,
  tmuxInstance: string,          // from the payload that carried the name
): void
```

It walks **every** leaf (via `scanPaneTree`), matches the full triple with the
empty-instance compatibility rule, and refreshes both `cachedName` and
`rebuild.sessionName`. Without the generation match, a rename broadcast from a
new tmux server would write the new name onto the old pane the reconciler is
about to mark dead — and pollute its `rebuild.sessionName`, which is what the
rebuild would then use. Update **every** caller to pass a generation:
`useMultiHostEventWs.ts:108` (the session's `tmux_instance`),
`spa/src/features/workspace/hooks.ts:183` (the renamed session's
`tmux_instance`, or the pane's own recorded instance when the daemon response
does not carry one), and the five call sites in
`spa/src/stores/useTabStore.test.ts:381,395,409,424,436`. Add these tests:

```ts
  it('a rename follows into the rebuild record', () => {
    const tab = seed()
    const store = useTabStore.getState()
    store.setPaneRebuild('h1', 'abc123', '111:1000', { kind: 'field', field: 'cwd', value: '/w' })
    store.updateSessionCache('h1', 'abc123', 'renamed', '111:1000')
    expect(rec(tab.id)?.sessionName).toBe('renamed')
  })

  it('a rename from a different generation is ignored', () => {
    const tab = seed('111:1000')
    useTabStore.getState().updateSessionCache('h1', 'abc123', 'stranger', '222:2000')
    const l = useTabStore.getState().tabs[tab.id].layout
    expect(l.type === 'leaf' && l.pane.content.kind === 'tmux-session'
      && l.pane.content.cachedName).toBe('dev')
  })

  it('renames a secondary split pane, not just the primary', () => {
    const split = splitTabWithSecondPane(
      createTab({ kind: 'tmux-session', hostId: 'h1', sessionCode: 'abc123',
        mode: 'terminal', cachedName: 'dev', tmuxInstance: '111:1000' }), 'p2')
    useTabStore.setState({ tabs: { [split.id]: split }, tabOrder: [split.id], activeTabId: split.id })
    useTabStore.getState().updateSessionCache('h1', 'abc123', 'renamed', '111:1000')
    expect(cachedNameOfPane(split.id, 'p2')).toBe('renamed')
  })
```

- [ ] **Step 4: Run tests**

Run: `pnpm --prefix spa exec vitest run`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
pnpm --prefix spa run lint
git add spa/src/types/tab.ts spa/src/stores/useTabStore.ts spa/src/stores/useTabStore.rebuild.test.ts spa/src/stores/useTabStore.test.ts spa/src/features/workspace/hooks.ts spa/src/hooks/useMultiHostEventWs.ts
git commit -m "feat(spa): add the per-pane rebuild record and its writer ranking"
```

---

### Task 10: Resume command composer

**Files:**
- Create: `spa/src/lib/rebuild/composer.ts`
- Create: `spa/src/lib/rebuild/composer.test.ts`

**Interfaces:**
- Produces: `composeResumeCommand(agentType: string, sessionId?: string): string`
  — returns `''` when no command can be composed. Task 11 calls it.

**Context:** spec §3.2 verified all six forms locally. A pane with no agent
gets **no** command — not a fallback — because nothing tells it which agent to
run (spec §4.7, §9.1).

- [ ] **Step 1: Write the failing test**

```ts
// spa/src/lib/rebuild/composer.test.ts
import { describe, it, expect } from 'vitest'
import { composeResumeCommand } from './composer'

describe('composeResumeCommand', () => {
  it.each([
    ['cc', 'S1', 'claude --resume S1'],
    ['cc', undefined, 'claude -c'],
    ['codex', 'S1', 'codex resume S1'],
    ['codex', undefined, 'codex resume --last'],
    ['opencode', 'S1', 'opencode -s S1'],
    ['opencode', undefined, 'opencode -c'],
  ])('%s / %s → %s', (agent, id, want) => {
    expect(composeResumeCommand(agent, id)).toBe(want)
  })

  it('returns empty for an unknown agent rather than guessing', () => {
    expect(composeResumeCommand('aider', 'S1')).toBe('')
    expect(composeResumeCommand('', undefined)).toBe('')
  })

  it('rejects a session id that could break out of the command', () => {
    expect(composeResumeCommand('cc', 'S1; rm -rf /')).toBe('claude -c')
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `pnpm --prefix spa exec vitest run composer`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement**

```ts
// spa/src/lib/rebuild/composer.ts

/** Session ids are UUIDs / ULIDs across all three agents; anything else is
 *  refused rather than interpolated into a shell command sent via send-keys. */
const SAFE_SESSION_ID = /^[A-Za-z0-9_-]{1,128}$/

const EXACT: Record<string, (id: string) => string> = {
  cc: (id) => `claude --resume ${id}`,
  codex: (id) => `codex resume ${id}`,
  opencode: (id) => `opencode -s ${id}`,
}

const CWD_SCOPED: Record<string, string> = {
  cc: 'claude -c',
  codex: 'codex resume --last',
  opencode: 'opencode -c',
}

/**
 * Compose the command that resumes `agentType`'s conversation. Returns '' when
 * the agent is unknown — a pane with no recorded agent rebuilds as a plain
 * shell rather than guessing (spec §4.7).
 */
export function composeResumeCommand(agentType: string, sessionId?: string): string {
  const fallback = CWD_SCOPED[agentType]
  if (!fallback) return ''
  if (sessionId && SAFE_SESSION_ID.test(sessionId)) return EXACT[agentType](sessionId)
  return fallback
}
```

- [ ] **Step 4: Run tests**

Run: `pnpm --prefix spa exec vitest run composer`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/rebuild/composer.ts spa/src/lib/rebuild/composer.test.ts
git commit -m "feat(spa): compose per-agent resume commands"
```

---

### Task 11: Write the record from the provenance envelope

**Files:**
- Create: `spa/src/lib/rebuild/provenance.ts`
- Create: `spa/src/lib/rebuild/provenance.test.ts`
- Modify: `spa/src/stores/useAgentStore.ts` (`handleNormalizedEvent`, ~line 130)
- Modify: `spa/src/hooks/useMultiHostEventWs.ts` (probe cwd for panes with none)

**Interfaces:**
- Consumes: `Provenance` (Task 4), `composeResumeCommand` (Task 10),
  `setPaneRebuild` (Task 9), `fetchSessionCwd` (`host-api.ts:157`).
- Produces:
  ```ts
  parseProvenance(detail: Record<string, unknown> | undefined): ParsedProvenance | null
  ```

**Context:** the SPA reads **only** `detail.pdx_provenance`. On a
proxy-collapsed event the outer `agent_type` names the session projection
winner while the rest of the detail describes the sender — reading the outer
field is exactly the mis-attribution the envelope exists to prevent
(spec §4.3.1).

- [ ] **Step 1: Write the failing test**

```ts
// spa/src/lib/rebuild/provenance.test.ts
import { describe, it, expect } from 'vitest'
import { parseProvenance } from './provenance'

const envelope = {
  owner_session_start: true, agent_type: 'codex', session_id: 'S1',
  cwd: '/w/p', tmux_pane_id: '%2', tmux_instance: '222:2000',
}

describe('parseProvenance', () => {
  it('parses a well-formed envelope', () => {
    expect(parseProvenance({ pdx_provenance: envelope })).toEqual({
      agentType: 'codex', sessionId: 'S1', cwd: '/w/p',
      tmuxPaneId: '%2', tmuxInstance: '222:2000',
    })
  })

  it('returns null when the flag is absent or false', () => {
    expect(parseProvenance({ pdx_provenance: { ...envelope, owner_session_start: false } })).toBeNull()
    expect(parseProvenance({ agent_type: 'cc', session_id: 'S1' })).toBeNull()
    expect(parseProvenance(undefined)).toBeNull()
  })

  it('returns null when the generation is unknown', () => {
    expect(parseProvenance({ pdx_provenance: { ...envelope, tmux_instance: '' } })).toBeNull()
  })

  it('ignores a non-object envelope', () => {
    expect(parseProvenance({ pdx_provenance: 'yes' })).toBeNull()
  })
})
```

Add a store-level test asserting the write path. It needs two local helpers,
written at the top of the new test file:

```ts
// spa/src/stores/useAgentStore.provenance.test.ts
import { useTabStore } from './useTabStore'
import { createTab } from '../types/tab'

/** Seed a single-pane tab bound to (h1, abc123) at the given generation. */
function seedTerminalPane(tmuxInstance: string) {
  const tab = createTab({
    kind: 'tmux-session', hostId: 'h1', sessionCode: 'abc123',
    mode: 'terminal', cachedName: 'dev', tmuxInstance,
  })
  useTabStore.setState({ tabs: { [tab.id]: tab }, tabOrder: [tab.id], activeTabId: tab.id })
  return tab
}

/** Read the rebuild record off that tab's single leaf pane. */
function recordOf(tabId: string) {
  const l = useTabStore.getState().tabs[tabId].layout
  return l.type === 'leaf' && l.pane.content.kind === 'tmux-session' ? l.pane.content.rebuild : undefined
}

it('writes the pane record on an owner session start', () => {
  const tab = seedTerminalPane('222:2000')
  useAgentStore.getState().handleNormalizedEvent('h1', 'abc123', {
    agent_type: 'codex', status: 'idle', raw_event_name: 'PdxSessionStart',
    broadcast_ts: 1, subagents: [],
    detail: { pdx_provenance: {
      owner_session_start: true, agent_type: 'codex', session_id: 'S1',
      cwd: '/w/p', tmux_pane_id: '%2', tmux_instance: '222:2000' } },
  })
  expect(recordOf(tab.id)?.resumeCommand).toBe('codex resume S1')
  expect(recordOf(tab.id)?.agent?.type).toBe('codex')
})

it('writes nothing for a proxy-collapsed event', () => {
  const tab = seedTerminalPane('222:2000')
  useAgentStore.getState().handleNormalizedEvent('h1', 'abc123', {
    agent_type: 'cc', status: 'idle', raw_event_name: 'PdxSessionStart',
    broadcast_ts: 1, subagents: [], detail: {},
  })
  expect(recordOf(tab.id)).toBeUndefined()
})
```

- [ ] **Step 2: Run to verify failure**

Run: `pnpm --prefix spa exec vitest run provenance`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement**

Write `parseProvenance` with strict shape validation (object, flag strictly
`true`, non-empty `agent_type` and `tmux_instance`). In
`handleNormalizedEvent`, after the existing `agentTypes` write, add:

```ts
      const prov = parseProvenance(event.detail)
      if (prov) {
        const now = Date.now()
        useTabStore.getState().setPaneRebuild(hostId, sessionCode, prov.tmuxInstance, {
          kind: 'agent-group',
          record: {
            tmuxInstance: prov.tmuxInstance,
            cwd: prov.cwd || undefined,
            cwdSource: prov.cwd ? 'agent-session-start' : undefined,
            agent: { type: prov.agentType, sessionId: prov.sessionId || undefined,
                     tmuxPaneId: prov.tmuxPaneId, updatedAt: now },
            resumeCommand: composeResumeCommand(prov.agentType, prov.sessionId) || undefined,
            capturedAt: now,
          },
        })
      }
```

Capture the shell-only baseline from **two** triggers, because a pane opened
after the session list has stabilised gets no further broadcast:

1. **On the sessions branch** of `useMultiHostEventWs`, for each live terminal
   pane whose record has no `cwd`.
2. **On pane attach** — a small `useEffect` in `SessionPaneContent` (or the
   hook it already uses) that fires once per `(hostId, sessionCode,
   tmuxInstance)` binding.

Both call `fetchSessionCwd` and write a `probe-cwd` patch, and both re-read the
pane when the request resolves, discarding the result if the binding changed
meanwhile. Deduplicate concurrent probes per binding so the two triggers cannot
fire twice for the same pane.

```ts
it('captures a cwd for a pane opened after the session list settled', async () => {
  const tab = seedTerminalPane('222:2000')          // no further sessions broadcast
  vi.mocked(fetchSessionCwd).mockResolvedValue('/w/late')
  renderPane(tab)
  await waitFor(() => expect(recordOf(tab.id)?.cwd).toBe('/w/late'))
  expect(recordOf(tab.id)?.cwdSource).toBe('pane-probe')
})

it('discards a probe whose pane was re-pointed while it was in flight', async () => {
  const tab = seedTerminalPane('222:2000')
  let resolve!: (v: string) => void
  vi.mocked(fetchSessionCwd).mockReturnValue(new Promise((r) => { resolve = r }))
  renderPane(tab)
  rebindPane(tab.id, 'p1', 'other-code')
  resolve('/w/stale')
  await Promise.resolve()
  expect(recordOf(tab.id)?.cwd).toBeUndefined()
})
```

Also flag `unverified`: when the reconnect projection reports an
`agentTypes[key]` that disagrees with `record.agent.type`, write
`{ kind: 'unverified', unverified: true }`.

- [ ] **Step 4: Run tests**

Run: `pnpm --prefix spa exec vitest run`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
pnpm --prefix spa run lint
git add spa/src/lib/rebuild/provenance.ts spa/src/lib/rebuild/provenance.test.ts spa/src/stores/useAgentStore.ts spa/src/stores/useAgentStore.provenance.test.ts spa/src/hooks/useMultiHostEventWs.ts spa/src/components/SessionPaneContent.tsx
git commit -m "feat(spa): populate the rebuild record from the provenance envelope"
```

---

# Phase 5 — Rebuild engine and its pane UI

### Task 12: Host-pinned transport and the rebuild engine

**Files:**
- Create: `spa/src/lib/rebuild/transport.ts`
- Create: `spa/src/lib/rebuild/engine.ts`
- Create: `spa/src/stores/useRebuildStore.ts`
- Create: `spa/src/lib/rebuild/engine.test.ts`
- Modify: `spa/src/lib/host-api.ts` (typed error carrying the HTTP status)

**Interfaces:**
- Consumes: `createSession`, `executeCommand`, `useTabStore.setPaneContent`,
  `useSessionStore`.
- Produces:
  ```ts
  interface RebuildPlan { createSession: boolean; applyCwd: boolean; runResume: boolean }
  interface StepResult { status: 'ok' | 'skipped' | 'failed'; error?: string }
  interface RebuildReport {
    hostId: string
    created?: { code: string; name: string; tmuxInstance: string }
    steps: { create: StepResult; resume: StepResult; repoint: StepResult }
  }
  interface RebuildDeps {
    createSession?: (hostId: string, name: string, cwd: string, mode: string) => Promise<Session>
    sendKeys?: (hostId: string, code: string, command: string) => Promise<void>
    repoint?: (tabId: string, paneId: string, session: Session) => void
  }
  rebuildPane(
    hostId: string, tabId: string, paneId: string,
    plan: RebuildPlan, deps?: RebuildDeps,
  ): Promise<RebuildReport>
  retryResume(paneId: string): Promise<RebuildReport>
  attachAnyway(paneId: string): Promise<RebuildReport>
  ```

**Context — three hard rules from review:**
1. **Never use `hostFetch`.** `getDaemonBase` falls back to the *active host*
   for an unknown `hostId` (`useHostStore.ts:143`), so a host removed during
   the operation would send the resume command to another machine.
2. **Resume before re-point.** `SessionPaneContent.tsx:70` unmounts
   `TerminatedPane` the moment `terminated` clears, so clearing it first would
   unmount the panel that reports the resume result.
3. **Retry only on HTTP 409.** The daemon returns 409 specifically for a
   duplicate name (`internal/module/session/handler.go:101-104`); 400 is
   validation and 500 is a create failure, and neither should trigger a rename
   retry.

- [ ] **Step 1: Write the failing test**

```ts
// spa/src/lib/rebuild/engine.test.ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { rebuildPane } from './engine'
import { useRebuildStore } from '../../stores/useRebuildStore'

const plan = { createSession: true, applyCwd: true, runResume: true }

// Fixtures. Every case seeds its own host + tab + pane + record; the
// absent-host guard is correct and would otherwise fail every happy path.
function seedHost(hostId: string, over: Partial<{ ip: string; port: number; token: string | null }> = {}) {
  useHostStore.setState({
    hosts: { [hostId]: { id: hostId, name: hostId, ip: '127.0.0.1', port: 7860, token: null, order: 0, ...over } },
    hostOrder: [hostId], activeHostId: hostId, runtime: { [hostId]: { status: 'connected', attachReady: true } },
  })
}

function seedPane(hostId: string, tabId: string, paneId: string, record: Partial<PaneRebuildRecord>) {
  const tab = {
    id: tabId, pinned: false, locked: false, createdAt: 0,
    layout: { type: 'leaf' as const, pane: { id: paneId, content: {
      kind: 'tmux-session' as const, hostId, sessionCode: 'old111', mode: 'terminal' as const,
      cachedName: 'dev', tmuxInstance: '111:1000', terminated: 'tmux-restarted' as const,
      rebuild: { sessionName: 'dev', tmuxInstance: '111:1000', capturedAt: 1, ...record },
    } } },
  }
  useTabStore.setState({ tabs: { [tabId]: tab }, tabOrder: [tabId], activeTabId: tabId })
}

describe('rebuildPane', () => {
  beforeEach(() => {
    useRebuildStore.setState({ operations: {}, lockedBy: null })
    seedHost('h1')
    seedPane('h1', 't1', 'p1', { cwd: '/w', resumeCommand: 'claude --resume S1',
      agent: { type: 'cc', sessionId: 'S1', updatedAt: 1 } })
    vi.unstubAllGlobals()
  })

  it('retries the name only on 409 and uses the returned name', async () => {
    const create = vi.fn()
      .mockRejectedValueOnce(Object.assign(new Error('conflict'), { status: 409 }))
      .mockResolvedValueOnce({ code: 'new1', name: 'dev-2', tmux_instance: '222:2000' })
    const report = await rebuildPane('h1', 't1', 'p1', plan, { createSession: create, sendKeys: vi.fn() })
    expect(create).toHaveBeenCalledTimes(2)
    expect(create.mock.calls[1][1]).toBe('dev-2')
    expect(report.created).toEqual({ code: 'new1', name: 'dev-2', tmuxInstance: '222:2000' })
  })

  it('does not retry on 400 or 500', async () => {
    for (const status of [400, 500]) {
      const create = vi.fn().mockRejectedValue(Object.assign(new Error('nope'), { status }))
      const report = await rebuildPane('h1', 't1', 'p1', plan, { createSession: create, sendKeys: vi.fn() })
      expect(create).toHaveBeenCalledTimes(1)
      expect(report.steps.create.status).toBe('failed')
    }
  })

  it('sends the resume before re-pointing the pane', async () => {
    const order: string[] = []
    const create = vi.fn(async () => { order.push('create'); return { code: 'new1', name: 'dev', tmux_instance: '222:2000' } })
    const sendKeys = vi.fn(async () => { order.push('resume') })
    const repoint = vi.fn(() => { order.push('repoint') })
    await rebuildPane('h1', 't1', 'p1', plan, { createSession: create, sendKeys, repoint })
    expect(order).toEqual(['create', 'resume', 'repoint'])
  })

  it('keeps the created session in the report and skips re-point when resume fails', async () => {
    const repoint = vi.fn()
    const report = await rebuildPane('h1', 't1', 'p1', plan, {
      createSession: vi.fn(async () => ({ code: 'new1', name: 'dev', tmux_instance: '222:2000' })),
      sendKeys: vi.fn(async () => { throw new Error('send-keys failed: 500') }),
      repoint,
    })
    expect(report.created?.code).toBe('new1')
    expect(report.steps.resume.status).toBe('failed')
    expect(repoint).not.toHaveBeenCalled()
    expect(useRebuildStore.getState().operations['p1'].report.created?.code).toBe('new1')
  })

  it('refuses to run against a host that is gone', async () => {
    const create = vi.fn()
    // 'gone' is deliberately not seeded — pinHost must throw before any request.
    const report = await rebuildPane('gone', 't1', 'p1', plan, { createSession: create, sendKeys: vi.fn() })
    expect(create).not.toHaveBeenCalled()
    expect(report.steps.create.status).toBe('failed')
  })

  it('aborts before the resume when the host disappears mid-operation', async () => {
    const sendKeys = vi.fn()
    const report = await rebuildPane('h1', 't1', 'p1', plan, {
      createSession: vi.fn(async () => { removeHostFromStore('h1'); return { code: 'new1', name: 'dev', tmux_instance: '222:2000' } }),
      sendKeys,
    })
    expect(sendKeys).not.toHaveBeenCalled()
    expect(report.created?.code).toBe('new1')
  })

  it('skips re-point when the pane binding changed mid-flight', async () => {
    const repoint = vi.fn()
    const report = await rebuildPane('h1', 't1', 'p1', plan, {
      createSession: vi.fn(async () => ({ code: 'new1', name: 'dev', tmux_instance: '222:2000' })),
      sendKeys: vi.fn(async () => { rebindPane('t1', 'p1', 'someone-else') }),
      repoint,
    })
    expect(repoint).not.toHaveBeenCalled()
    expect(report.steps.repoint.status).toBe('skipped')
  })
})
```

(`removeHostFromStore` and `rebindPane` are local helpers in the test file that
mutate `useHostStore` / `useTabStore` directly.)

- [ ] **Step 2: Run to verify failure**

Run: `pnpm --prefix spa exec vitest run engine`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement**

Add to `host-api.ts`:

```ts
export class HostApiError extends Error {
  readonly status: number
  constructor(status: number, statusText: string) {
    super(`${status} ${statusText}`)
    this.status = status
  }
}
```

and throw it from `createSession` instead of the plain `Error`.

`transport.ts` must provide the **request functions themselves**, not just a
base URL — the existing `createSession` (`host-api.ts:103`) and
`executeCommand` (`execute-command.ts:4`) both go through `hostFetch`, which is
what carries the active-host fallback:

```ts
// spa/src/lib/rebuild/transport.ts
import { useHostStore } from '../../stores/useHostStore'
import { HostApiError } from '../host-api'
import type { Session } from '../host-api'

export interface PinnedTransport {
  hostId: string
  /** Throws if the host's ip/port/token changed since the pin was taken. */
  assertUnchanged(): void
  createSession(name: string, cwd: string, mode: string): Promise<Session>
  sendKeys(sessionCode: string, command: string): Promise<void>
}

/** Pin a host's address once, for the lifetime of one rebuild operation.
 *  Throws when the host does not exist — deliberately, because hostFetch's
 *  getDaemonBase (useHostStore.ts:143) would otherwise fall back to the ACTIVE
 *  host and run the resume command on another machine. */
export function pinHost(hostId: string): PinnedTransport {
  const host = useHostStore.getState().hosts[hostId]
  if (!host) throw new Error(`host ${hostId} is not configured`)
  const pinned = { ip: host.ip, port: host.port, token: host.token ?? null }
  const base = `http://${pinned.ip}:${pinned.port}`

  const request = async (path: string, body: unknown): Promise<Response> => {
    assertUnchanged()
    const headers: Record<string, string> = { 'Content-Type': 'application/json' }
    if (pinned.token) headers['Authorization'] = `Bearer ${pinned.token}`
    return fetch(`${base}${path}`, { method: 'POST', headers, body: JSON.stringify(body) })
  }

  function assertUnchanged() {
    const now = useHostStore.getState().hosts[hostId]
    if (!now || now.ip !== pinned.ip || now.port !== pinned.port || (now.token ?? null) !== pinned.token) {
      throw new Error(`host ${hostId} changed during the operation`)
    }
  }

  return {
    hostId,
    assertUnchanged,
    async createSession(name, cwd, mode) {
      const res = await request('/api/sessions', { name, cwd, mode })
      if (!res.ok) throw new HostApiError(res.status, res.statusText)
      return res.json()
    },
    async sendKeys(sessionCode, command) {
      const res = await request(`/api/sessions/${sessionCode}/send-keys`, { keys: command + '\n' })
      if (!res.ok) throw new HostApiError(res.status, res.statusText)
    },
  }
}
```

The auth header must match what `useHostStore.getAuthHeaders` produces —
read it and copy the exact header name and format rather than assuming
`Authorization: Bearer`.

`engine.ts`'s default deps are `pinHost(hostId).createSession` and
`.sendKeys`; the injected `deps` in the tests above override them. The
`HostApiError` added to `host-api.ts` is thrown by **both** the pinned
transport and the legacy `createSession`, so the 409 detection works either
way.

Add one test that injects **no** deps and stubs `fetch` instead, so the
production path is covered:

```ts
it('uses the pinned host and retries only on 409 through the real transport', async () => {
  seedHost('h1', { ip: '10.0.0.9', port: 7860, token: 'tk' })
  seedPane('h1', 't1', 'p1', { sessionName: 'dev', cwd: '/w', resumeCommand: 'claude -c' })
  const calls: string[] = []
  vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit) => {
    calls.push(url)
    if (url.endsWith('/api/sessions') && calls.filter((u) => u.endsWith('/api/sessions')).length === 1) {
      return new Response('session already exists: dev', { status: 409, statusText: 'Conflict' })
    }
    if (url.endsWith('/api/sessions')) {
      return new Response(JSON.stringify({ code: 'new1', name: 'dev-2', tmux_instance: '222:2000' }), { status: 200 })
    }
    return new Response('{}', { status: 200 })
  }))

  const report = await rebuildPane('h1', 't1', 'p1', plan)
  expect(calls.every((u) => u.startsWith('http://10.0.0.9:7860'))).toBe(true)
  expect(calls.filter((u) => u.endsWith('/api/sessions'))).toHaveLength(2)
  expect(calls.some((u) => u.endsWith('/api/sessions/new1/send-keys'))).toBe(true)
  expect(report.created?.name).toBe('dev-2')
})

it('stops at the retry cap', async () => {
  seedHost('h1'); seedPane('h1', 't1', 'p1', { sessionName: 'dev' })
  vi.stubGlobal('fetch', vi.fn(async () => new Response('dup', { status: 409, statusText: 'Conflict' })))
  const report = await rebuildPane('h1', 't1', 'p1', plan)
  expect(report.steps.create.status).toBe('failed')
  expect((globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls).toHaveLength(5)
})
```

`engine.ts` implements the four steps with the injectable deps used above
(defaults wired to the real API), writing progress into `useRebuildStore`
throughout. `useRebuildStore` holds `operations: Record<paneId, {...}>` and a
single `lockedBy: string | null` used by Task 14.

- [ ] **Step 4: Run tests**

Run: `pnpm --prefix spa exec vitest run`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
pnpm --prefix spa run lint
git add spa/src/lib/rebuild/transport.ts spa/src/lib/rebuild/engine.ts spa/src/lib/rebuild/engine.test.ts spa/src/stores/useRebuildStore.ts spa/src/lib/host-api.ts
git commit -m "feat(spa): rebuild engine with a host-pinned transport"
```

---

### Task 13: The action set UI

**Files:**
- Create: `spa/src/components/RebuildActionSet.tsx`
- Create: `spa/src/components/RebuildActionSet.test.tsx`
- Modify: `spa/src/components/TerminatedPane.tsx`
- Modify: `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json`

**Interfaces:**
- Consumes: `PaneRebuildRecord`, `rebuildPane`, `useRebuildStore`.
- Produces: `<RebuildActionSet tabId paneId record onRebuild />`, reused by
  Task 15's popover.

**Context — inline editing must reuse `EditableCwdCell`'s hard-won guards
(alpha.324):** a `committedRef` against double submit, `disabled` while busy,
and `compositionRef` + `e.nativeEvent.isComposing` so an IME Enter does not
commit. Read `spa/src/components/settings/EditableCwdCell.tsx` before
writing this.

- [ ] **Step 1: Write the failing test**

```tsx
// spa/src/components/RebuildActionSet.test.tsx
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { RebuildActionSet } from './RebuildActionSet'

const record = {
  sessionName: 'dev', tmuxInstance: '111:1000', cwd: '/w/p',
  agent: { type: 'cc', sessionId: 'S1', updatedAt: 1 },
  resumeCommand: 'claude --resume S1', capturedAt: 1,
}

describe('RebuildActionSet', () => {
  it('checks all three rows by default', () => {
    render(<RebuildActionSet tabId="t1" paneId="p1" record={record} onRebuild={vi.fn()} />)
    screen.getAllByRole('checkbox').forEach((cb) => expect(cb).toBeChecked())
  })

  it('disables and unchecks the resume row when there is no command', () => {
    render(<RebuildActionSet tabId="t1" paneId="p1" record={{ ...record, resumeCommand: undefined, agent: undefined }} onRebuild={vi.fn()} />)
    const resume = screen.getByRole('checkbox', { name: /resume/i })
    expect(resume).toBeDisabled()
    expect(resume).not.toBeChecked()
  })

  it('passes the unchecked rows through to the plan', () => {
    const onRebuild = vi.fn()
    render(<RebuildActionSet tabId="t1" paneId="p1" record={record} onRebuild={onRebuild} />)
    fireEvent.click(screen.getByRole('checkbox', { name: /resume/i }))
    fireEvent.click(screen.getByRole('button', { name: /rebuild/i }))
    expect(onRebuild).toHaveBeenCalledWith({ createSession: true, applyCwd: true, runResume: false })
  })

  it('does not commit an edit on an IME Enter', () => {
    const onEdit = vi.fn()
    render(<RebuildActionSet tabId="t1" paneId="p1" record={record} onRebuild={vi.fn()} onEdit={onEdit} />)
    fireEvent.doubleClick(screen.getByText('/w/p'))
    const input = screen.getByDisplayValue('/w/p')
    fireEvent.change(input, { target: { value: '/w/other' } })
    fireEvent.keyDown(input, { key: 'Enter', isComposing: true })
    expect(onEdit).not.toHaveBeenCalled()
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(onEdit).toHaveBeenCalledWith('cwd', '/w/other')
  })

  it('shows retry actions after a failed resume instead of a fresh rebuild', () => {
    render(<RebuildActionSet tabId="t1" paneId="p1" record={record} onRebuild={vi.fn()}
      operation={{ report: { hostId: 'h1', created: { code: 'new1', name: 'dev', tmuxInstance: '222:2000' },
        steps: { create: { status: 'ok' }, resume: { status: 'failed', error: 'send-keys failed: 500' }, repoint: { status: 'skipped' } } } }} />)
    expect(screen.getByRole('button', { name: /retry resume/i })).toBeEnabled()
    expect(screen.getByRole('button', { name: /attach anyway/i })).toBeEnabled()
    expect(screen.getByText(/dev/)).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `pnpm --prefix spa exec vitest run RebuildActionSet`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement**

Build `RebuildActionSet` with the three rows, the checkbox state, the inline
editors (copying `EditableCwdCell`'s guards), and the operation-aware footer
(Rebuild / Retry resume + Attach anyway). Render it in `TerminatedPane` above
the existing `SessionPickerList`.

Row rules — each is a spec requirement, and each needs its own assertion:

| Condition | Behaviour | Spec |
|---|---|---|
| pane is `terminated` | "Create tmux session" is checked and cannot be unchecked | §4.9 |
| `record.cwd` missing | cwd row disabled **and** unchecked; session is created in the daemon default | §4.9 |
| `record.resumeCommand` missing | resume row disabled and unchecked; hint `rebuild.no_agent_hint` explains a shell will be created | §4.7, §7 |
| `record.unverified` | resume row rendered but **unchecked by default**, hint `rebuild.unverified_hint` | §9.1, §7 |
| `terminated === 'host-removed'` | Rebuild hidden, `rebuild.host_removed_hint` shown | §4.8 |

Also surface the §7 limits as copy: `rebuild.limits_agent_only`,
`rebuild.limits_minimal_flags`, `rebuild.limits_cwd_scoped`,
`rebuild.limits_multi_pane`, `rebuild.limits_local_storage` — rendered as a
collapsible note under the action set, in both locales.

New i18n keys (both locales):
`rebuild.create_session`, `rebuild.working_directory`, `rebuild.run_resume`,
`rebuild.button`, `rebuild.retry_resume`, `rebuild.attach_anyway`,
`rebuild.host_removed_hint`, `rebuild.no_agent_hint`, `rebuild.unverified_hint`,
`rebuild.limits_agent_only`, `rebuild.limits_minimal_flags`,
`rebuild.limits_cwd_scoped`, `rebuild.limits_multi_pane`,
`rebuild.limits_local_storage`.

- [ ] **Step 4: Run tests**

Run: `pnpm --prefix spa exec vitest run`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
pnpm --prefix spa run lint && pnpm --prefix spa run build
git add spa/src/components/RebuildActionSet.tsx spa/src/components/RebuildActionSet.test.tsx spa/src/components/TerminatedPane.tsx spa/src/locales/en.json spa/src/locales/zh-TW.json
git commit -m "feat(spa): rebuild action set on terminated panes"
```

---

### Task 14: Shared operation lock across the legacy snapshot actions

**Files:**
- Modify: `spa/src/stores/useRebuildStore.ts` (expose `acquire` / `release`)
- Modify: `spa/src/components/settings/SnapshotSettingsSection.tsx:249,279`
- Test: `spa/src/stores/useRebuildStore.lock.test.ts`

**Interfaces:**
- Produces: `acquireOperationLock(owner: string): boolean`,
  `releaseOperationLock(owner: string): void`.

**Context:** Snapshot today has only a component-local `busyRef`
(`SnapshotSettingsSection.tsx:249`), and `restoreAll` replaces the entire tab
snapshot (`restore.ts:457`; `:448` is `ensureSessions`) — which would overwrite an in-flight rebuild's
re-point. The lock must ship with the engine, not with the Phase 7 UI.

- [ ] **Step 1: Write the failing test**

```ts
// spa/src/stores/useRebuildStore.lock.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { useRebuildStore } from './useRebuildStore'

describe('operation lock', () => {
  beforeEach(() => useRebuildStore.setState({ operations: {}, lockedBy: null }))

  it('grants to the first caller and refuses the second', () => {
    expect(useRebuildStore.getState().acquireOperationLock('rebuild:p1')).toBe(true)
    expect(useRebuildStore.getState().acquireOperationLock('snapshot:restoreAll')).toBe(false)
  })

  it('is re-entrant for the same owner (undo → restoreAll)', () => {
    expect(useRebuildStore.getState().acquireOperationLock('snapshot:undo')).toBe(true)
    expect(useRebuildStore.getState().acquireOperationLock('snapshot:undo')).toBe(true)
  })

  it('releases only for the holder', () => {
    useRebuildStore.getState().acquireOperationLock('rebuild:p1')
    useRebuildStore.getState().releaseOperationLock('snapshot:restoreAll')
    expect(useRebuildStore.getState().lockedBy).toBe('rebuild:p1')
    useRebuildStore.getState().releaseOperationLock('rebuild:p1')
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `pnpm --prefix spa exec vitest run useRebuildStore.lock`
Expected: FAIL — `acquireOperationLock` is not a function.

- [ ] **Step 3: Implement**

Use an **outermost-acquire token model**, not per-call acquisition:
`acquireOperationLock(owner)` returns a token when it grants and `null` when
another owner holds it; a nested call by the same owner returns a *re-entry*
token that does not release the lock on its own. Only the outermost token
releases. This is what lets `undoLastRestore` → `restoreAll`
(`restore.ts:470` → `:424`) nest without the inner call dropping the lock.

Wrap **every** engine entry point — `rebuildPane`, `retryResume`,
`attachAnyway` and Task 16's batch runner — plus all five legacy actions
(`rebuildAllSessions`, `restoreTabLayout`, `restoreAll`, `undoLastRestore`,
and the "restore everything" entry at `SnapshotSettingsSection.tsx:279`).
Owner strings: `rebuild:<paneId>` for single-pane operations,
`rebuild:batch` for the batch runner, `snapshot:<action>` for the legacy ones.

⚠️ Two operations on the **same pane** must not both proceed just because
they share an owner string. `rebuild:<paneId>` makes the owner pane-specific,
so a second operation on the same pane is refused by the re-entrancy rule
returning a re-entry token rather than starting concurrent work — the engine
must therefore check "am I already running for this pane" from
`useRebuildStore.operations[paneId].status` before acquiring, and refuse.

Disable every button whose owner is not the current holder while `lockedBy` is
set. Add:

```ts
  it('refuses a second concurrent operation on the same pane', async () => {
    const first = rebuildPane('h1', 't1', 'p1', plan, { createSession: neverResolves, sendKeys: vi.fn() })
    const second = await rebuildPane('h1', 't1', 'p1', plan, { createSession: vi.fn(), sendKeys: vi.fn() })
    expect(second.steps.create.status).toBe('failed')
    void first
  })

  it('blocks a legacy snapshot action while a rebuild holds the lock', () => {
    useRebuildStore.getState().acquireOperationLock('rebuild:p1')
    expect(useRebuildStore.getState().acquireOperationLock('snapshot:restoreAll')).toBeNull()
  })

  it('nested undo → restoreAll keeps the lock until the outermost release', () => {
    const outer = useRebuildStore.getState().acquireOperationLock('snapshot:undo')
    const inner = useRebuildStore.getState().acquireOperationLock('snapshot:undo')
    useRebuildStore.getState().releaseOperationLock(inner!)
    expect(useRebuildStore.getState().lockedBy).toBe('snapshot:undo')
    useRebuildStore.getState().releaseOperationLock(outer!)
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })
```

and update the three tests written in Step 1 to the token signature.

- [ ] **Step 4: Run tests**

Run: `pnpm --prefix spa exec vitest run`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
pnpm --prefix spa run lint
git add spa/src/stores/useRebuildStore.ts spa/src/stores/useRebuildStore.lock.test.ts spa/src/components/settings/SnapshotSettingsSection.tsx spa/src/lib/rebuild/engine.ts
git commit -m "feat(spa): serialize rebuild against the legacy snapshot actions"
```

---

# Phase 6 — Tab-name popover

### Task 15: Per-pane detail blocks and the new entry point

**Files:**
- Modify: `spa/src/features/workspace/hooks.ts:97,173`
- Modify: `spa/src/components/RenamePopover.tsx`
- Modify: `spa/src/App.tsx:317-326`
- Test: `spa/src/components/RenamePopover.rebuild.test.tsx`

**Interfaces:**
- Consumes: `RebuildActionSet` (Task 13), `collectLeaves`
  (`spa/src/lib/pane-tree.ts:136`).

**Context:** the popover's entry point looks only at the tab's **primary** pane
(`hooks.ts:97`) — it does not open when that pane is an editor or a terminated
session even if another pane is a live terminal — and `hooks.ts:173` renames a
single target. This task changes the entry point, not just the content.

- [ ] **Step 1: Write the failing test**

```tsx
// spa/src/components/RenamePopover.rebuild.test.tsx
//
// Fixtures — each case seeds its own tab; `props` is the popover's required
// prop set. Reuse the seedHost/seedPane helpers written for Task 12 by
// exporting them from a shared test-utils module in that task.
function seedSplitTab(contents: PaneContent[]) {
  const panes = contents.map((content, i) => ({ id: `p${i + 1}`, content }))
  return {
    id: 't1', pinned: false, locked: false, createdAt: 0,
    layout: panes.length === 1
      ? { type: 'leaf' as const, pane: panes[0] }
      : { type: 'split' as const, id: 's1', direction: 'h' as const,
          children: panes.map((pane) => ({ type: 'leaf' as const, pane })),
          sizes: panes.map(() => 1 / panes.length) },
  }
}

const props = {
  anchorRect: new DOMRect(0, 0, 100, 20),
  currentName: 'dev',
  onConfirm: vi.fn(async () => {}),
  onCancel: vi.fn(),
}

it('opens on a real double-click when the primary pane is an editor', async () => {
  const tab = seedSplitTab([
    { kind: 'editor', source: 'local', filePath: '/a.md' },
    { kind: 'tmux-session', hostId: 'h1', sessionCode: 'abc123', mode: 'terminal',
      cachedName: 'dev', tmuxInstance: '111:1000' },
  ])
  useTabStore.setState({ tabs: { t1: tab }, tabOrder: ['t1'], activeTabId: 't1' })
  render(<TabBarHarness />)
  fireEvent.doubleClick(screen.getByRole('tab', { name: /dev/ }))
  expect(await screen.findByDisplayValue('dev')).toBeInTheDocument()
})

it('collects one target per terminal pane', () => {
  const tab = seedSplitTab([
    { kind: 'editor', source: 'local', filePath: '/a.md' },
    { kind: 'tmux-session', hostId: 'h1', sessionCode: 'abc123', mode: 'terminal',
      cachedName: 'dev', tmuxInstance: '111:1000' },
  ])
  expect(collectRenameTargets(tab)).toHaveLength(1)
  expect(collectRenameTargets(tab)[0].sessionCode).toBe('abc123')
})

it('renders one block per terminal pane with independent targets', () => {
  const tab = seedSplitTab([
    { kind: 'tmux-session', hostId: 'h1', sessionCode: 'aaa', mode: 'terminal', cachedName: 'one', tmuxInstance: '111:1000' },
    { kind: 'tmux-session', hostId: 'h1', sessionCode: 'bbb', mode: 'terminal', cachedName: 'two', tmuxInstance: '111:1000' },
  ])
  render(<RenamePopover {...props} tab={tab} />)
  expect(screen.getByDisplayValue('one')).toBeInTheDocument()
  expect(screen.getByDisplayValue('two')).toBeInTheDocument()
})

it('editing cwd does not submit the rename', () => {
  const onConfirm = vi.fn()
  render(<RenamePopover {...props} onConfirm={onConfirm} />)
  const cwd = screen.getByDisplayValue('/w/p')
  fireEvent.keyDown(cwd, { key: 'Enter' })
  expect(onConfirm).not.toHaveBeenCalled()
})
```

- [ ] **Step 2: Run to verify failure**

Run: `pnpm --prefix spa exec vitest run RenamePopover.rebuild`
Expected: FAIL — `collectRenameTargets` does not exist.

- [ ] **Step 3: Implement**

Export `collectRenameTargets(tab)` from `hooks.ts` using `collectLeaves` +
a `mode === 'terminal'` filter, and open the popover whenever it returns at
least one target. Render one `RebuildActionSet`-derived block per target inside
`RenamePopover`, keyed by `paneId`. A live pane's name row calls the existing
daemon rename; a dead pane's name row writes `rebuild.sessionName`. Stop
propagation of `Enter` inside the cwd / resume inputs.

- [ ] **Step 4: Run tests**

Run: `pnpm --prefix spa exec vitest run`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
pnpm --prefix spa run lint && pnpm --prefix spa run build
git add spa/src/features/workspace/hooks.ts spa/src/components/RenamePopover.tsx spa/src/components/RenamePopover.rebuild.test.tsx spa/src/App.tsx
git commit -m "feat(spa): show and edit rebuild details from the tab popover"
```

---

# Phase 7 — Snapshot batch view

### Task 16: Records table and "Rebuild all"

**Files:**
- Create: `spa/src/lib/rebuild/batch.ts`
- Create: `spa/src/lib/rebuild/batch.test.ts`
- Modify: `spa/src/components/settings/SnapshotSettingsSection.tsx`
- Modify: `spa/src/locales/en.json`, `spa/src/locales/zh-TW.json`

**Interfaces:**
- Produces:
  ```ts
  interface PaneRef { paneId: string; tabId: string; hostId: string; sessionCode: string; tmuxInstance: string }
  interface BatchGroup extends PaneRef {
    paneIds: string[]          // every pane re-pointed to this group's result
    sourcePaneId: string       // whose record won the conflict resolution
    record: PaneRebuildRecord
    plan: RebuildPlan
  }
  groupForBatch(panes: (PaneRef & { record: PaneRebuildRecord })[]):
    { groups: BatchGroup[]; excluded: PaneRef[] }
  ```

**Context:** grouping key is `(hostId, tmuxInstance, sessionCode)` — the
instance stays in the key because the same code under two different non-empty
instances is genuinely two different historical sessions. Panes with an
unknown (`''`) instance are excluded from the automatic batch. Conflicting
hand-edits inside one group resolve to the latest `capturedAt`. Unverified
records are skipped.

- [ ] **Step 1: Write the failing test**

```ts
// spa/src/lib/rebuild/batch.test.ts
import { describe, it, expect } from 'vitest'
import { groupForBatch } from './batch'

const pane = (paneId: string, over: Record<string, unknown> = {}) => ({
  paneId, tabId: 't1', hostId: 'h1', sessionCode: 'abc', tmuxInstance: '111:1000',
  record: { sessionName: 'dev', tmuxInstance: '111:1000', cwd: '/w', capturedAt: 1,
    agent: { type: 'cc', sessionId: 'S1', updatedAt: 1 }, resumeCommand: 'claude --resume S1' },
  ...over,
})

describe('groupForBatch', () => {
  it('merges two panes on the same dead session into one group', () => {
    const { groups } = groupForBatch([pane('p1'), pane('p2')])
    expect(groups).toHaveLength(1)
    expect(groups[0].paneIds).toEqual(['p1', 'p2'])
  })

  it('keeps the same code under different generations apart', () => {
    const { groups } = groupForBatch([pane('p1'), pane('p2', { tmuxInstance: '222:2000' })])
    expect(groups).toHaveLength(2)
  })

  it('excludes panes with an unknown generation', () => {
    const { groups, excluded } = groupForBatch([pane('p1'), pane('p2', { tmuxInstance: '' })])
    expect(groups).toHaveLength(1)
    expect(excluded.map((p) => p.paneId)).toEqual(['p2'])
  })

  it('resolves conflicting hand-edits to the latest capturedAt', () => {
    const older = pane('p1')
    const newer = pane('p2', { record: { ...pane('p2').record, cwd: '/w/newer', capturedAt: 9 } })
    const { groups } = groupForBatch([older, newer])
    expect(groups[0].record.cwd).toBe('/w/newer')
    expect(groups[0].sourcePaneId).toBe('p2')
  })

  it('skips the exact resume for unverified records', () => {
    const { groups } = groupForBatch([pane('p1', { record: { ...pane('p1').record, unverified: true } })])
    expect(groups[0].plan.runResume).toBe(false)
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `pnpm --prefix spa exec vitest run batch`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement**

Write `groupForBatch`, then rebuild the Snapshot section's table to read the
per-tab records: one row per terminal pane with the existing four-state health
indicator, a "Rebuild all" button running each group through `rebuildPane` and
re-pointing every pane in the group, and a separate "needs attention" list for
the excluded panes with a single-pane Rebuild each. Label the legacy actions
shell-only. All actions go through the Task 14 lock.

New i18n keys: `rebuild.batch_title`, `rebuild.batch_run`,
`rebuild.batch_needs_attention`, `rebuild.legacy_shell_only`,
`rebuild.batch_conflict_source`.

Grouping alone is not enough coverage — add orchestration tests that run the
batch through the engine:

```ts
it('creates one session for a group and re-points every member, across tabs', async () => {
  seedHost('h1')
  seedPane('h1', 't1', 'p1', { sessionName: 'dev' })
  seedPane('h1', 't2', 'p2', { sessionName: 'dev' })   // same dead session, other tab
  const create = vi.fn(async () => ({ code: 'new1', name: 'dev', tmux_instance: '222:2000' }))
  const sendKeys = vi.fn()

  await runBatchRebuild({ createSession: create, sendKeys })

  expect(create).toHaveBeenCalledTimes(1)
  expect(sendKeys).toHaveBeenCalledTimes(1)
  expect(sessionCodeOfPane('t1', 'p1')).toBe('new1')
  expect(sessionCodeOfPane('t2', 'p2')).toBe('new1')
})

it('re-verifies each member binding before re-pointing it', async () => {
  seedHost('h1')
  seedPane('h1', 't1', 'p1', { sessionName: 'dev' })
  seedPane('h1', 't2', 'p2', { sessionName: 'dev' })
  await runBatchRebuild({
    createSession: vi.fn(async () => { rebindPane('t2', 'p2', 'someone-else'); return { code: 'new1', name: 'dev', tmux_instance: '222:2000' } }),
    sendKeys: vi.fn(),
  })
  expect(sessionCodeOfPane('t1', 'p1')).toBe('new1')
  expect(sessionCodeOfPane('t2', 'p2')).toBe('someone-else')
})

it('names the winning pane before running when hand-edits conflict', () => {
  seedPane('h1', 't1', 'p1', { sessionName: 'dev', cwd: '/a' })
  seedPane('h1', 't2', 'p2', { sessionName: 'dev', cwd: '/b', capturedAt: 9 })
  render(<SnapshotSettingsSection />)
  expect(screen.getByText(/\/b/)).toBeInTheDocument()
  expect(screen.getByTestId('batch-conflict-source')).toHaveTextContent('p2')
})

it('refuses to start while a single-pane rebuild holds the lock', async () => {
  useRebuildStore.getState().acquireOperationLock('rebuild:p1')
  const report = await runBatchRebuild({ createSession: vi.fn(), sendKeys: vi.fn() })
  expect(report.status).toBe('blocked')
})
```

The batch runner takes the lock **once** as `rebuild:batch` and passes its
token down to each group's engine call rather than letting each call acquire
its own.

- [ ] **Step 4: Run tests**

Run: `pnpm --prefix spa exec vitest run`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
pnpm --prefix spa run lint && pnpm --prefix spa run build
git add spa/src/lib/rebuild/batch.ts spa/src/lib/rebuild/batch.test.ts spa/src/components/settings/SnapshotSettingsSection.tsx spa/src/locales/en.json spa/src/locales/zh-TW.json
git commit -m "feat(spa): batch rebuild view over the per-tab records"
```

---

## Manual verification (after Phase 6, on the Air)

The reboot path cannot be unit-tested. Run this after **Phase 6** — step 1
uses the popover, which Phase 6 builds; everything else is available from
Phase 5, so run steps 2-6 early if Phase 5 ships alone:

1. Open a tab on a session running Claude Code; confirm the popover shows the
   agent, cwd and `claude --resume <id>`.
2. `tmux kill-server` on the host.
3. Confirm the pane flips to "tmux restarted" **rather than** silently
   attaching to a reused code, and that no other pane is wrongly marked.
4. Press Rebuild; confirm the session is recreated with the original name, in
   the original directory, with the agent resumed into its previous
   conversation.
5. Repeat with codex and opencode.
6. Repeat with the resume row unchecked (expect a plain shell).
