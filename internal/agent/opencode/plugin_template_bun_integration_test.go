package opencode

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestRenderManagedPlugin_BunRuntimeEmitsStdin proves the rendered emit()
// helper actually drives Bun.spawn correctly at runtime. Plan
// 2026-04-29 §T1: existing string-shape tests cannot catch the
// TypeError: ERR_INVALID_ARG_TYPE that ships with stdin: <raw string>,
// because they never invoke a real Bun. We render the plugin against
// a stub pdx shell binary, append an async IIFE that fires one
// session.created event, run the result with `bun <script.mjs>`, and
// assert the stub captured the JSON payload on stdin.
//
// The test skips on Windows, on hosts without /bin/sh, on hosts
// without bun on PATH, and on hosts where `bun --version` fails. Each
// gate is a skip rather than a fail so a single `go test ./...`
// invocation works across CI environments.
func TestRenderManagedPlugin_BunRuntimeEmitsStdin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("opencode plugin runtime is POSIX-only; skipping real-Bun integration test")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skipf("/bin/sh unavailable (%v); skipping real-Bun integration test", err)
	}
	bunPath, err := exec.LookPath("bun")
	if err != nil {
		t.Skip("bun not on PATH; skipping real-Bun integration test")
	}
	if out, err := exec.Command(bunPath, "--version").Output(); err != nil || strings.TrimSpace(string(out)) == "" {
		t.Skipf("bun --version failed (%v); skipping real-Bun integration test", err)
	}

	tmp := t.TempDir()
	stubPath := filepath.Join(tmp, "pdx-stub")
	capturePath := filepath.Join(tmp, "stdin-capture")
	stubBody := "#!/bin/sh\ncat > \"$PDX_TEST_STDIN_CAPTURE\"\n"
	if err := os.WriteFile(stubPath, []byte(stubBody), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	// Belt-and-braces chmod in case umask stripped exec bit.
	if err := os.Chmod(stubPath, 0o755); err != nil {
		t.Fatalf("chmod stub: %v", err)
	}

	body := renderManagedPlugin(stubPath)
	tail := `
;(async () => {
  const hooks = await PurdexOpenCodeHooks()
  await hooks.event({
    event: {
      type: 'session.created',
      properties: { sessionID: 'test-session' },
    },
  })
})()
`
	scriptPath := filepath.Join(tmp, "plugin.mjs")
	if err := os.WriteFile(scriptPath, []byte(body+tail), 0o644); err != nil {
		t.Fatalf("write plugin.mjs: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bunPath, scriptPath)
	cmd.Env = append(os.Environ(), "PDX_TEST_STDIN_CAPTURE="+capturePath)
	output, runErr := cmd.CombinedOutput()
	if runErr != nil {
		// Classify the failure: a TypeError mentioning ERR_INVALID_ARG_TYPE
		// (or the literal "stdio must be an array") is the pre-fix red
		// signal. Any other failure (envelope mismatch, deadlock, syntax
		// error) means the harness itself is wrong; surface it explicitly
		// so a future contributor can repair it without conflating with
		// the actual regression.
		out := string(output)
		if strings.Contains(out, "ERR_INVALID_ARG_TYPE") || strings.Contains(out, "stdio must be an array") {
			t.Fatalf("pre-fix Bun TypeError observed; apply T2 stdin-pipe fix to turn this green: %s", out)
		}
		t.Fatalf("unexpected bun failure: err=%v output=%s", runErr, out)
	}

	captured, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("read capture: %v (bun output: %s)", err, output)
	}
	var payload map[string]any
	if err := json.Unmarshal(captured, &payload); err != nil {
		t.Fatalf("unmarshal captured stdin: %v (raw=%q)", err, string(captured))
	}
	if got := payload["session_id"]; got != "test-session" {
		t.Fatalf("captured stdin session_id = %v, want %q (raw=%q)", got, "test-session", string(captured))
	}
}

// TestRenderManagedPlugin_BunRuntimeGatesChildSessionLifecycle drives a
// realistic parent+subagent lifecycle through the real rendered JS under
// Bun and proves the child-session gate holds end-to-end (guards against
// "mirror and JS drift wrong together"). The stub pdx appends every
// invocation as `eventName<TAB>stdin` JSONL; the event name comes from
// argv[4] of [pdxPath,'hook','--agent','opencode',eventName]. emit() awaits
// proc.exited, so appends are ordered and non-interleaved.
//
// Sequence: parent created -> child created(parentID) -> child idle ->
// child error -> child deleted -> parent idle -> parent deleted. Only the
// three parent-level events (PdxSessionStart, PdxStop, PdxSessionEnd) must
// reach the stub; every child-derived event must be gated.
func TestRenderManagedPlugin_BunRuntimeGatesChildSessionLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("opencode plugin runtime is POSIX-only; skipping real-Bun integration test")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skipf("/bin/sh unavailable (%v); skipping real-Bun integration test", err)
	}
	bunPath, err := exec.LookPath("bun")
	if err != nil {
		t.Skip("bun not on PATH; skipping real-Bun integration test")
	}
	if out, err := exec.Command(bunPath, "--version").Output(); err != nil || strings.TrimSpace(string(out)) == "" {
		t.Skipf("bun --version failed (%v); skipping real-Bun integration test", err)
	}

	tmp := t.TempDir()
	stubPath := filepath.Join(tmp, "pdx-stub")
	capturePath := filepath.Join(tmp, "events-capture")
	// argv[4] is the event name; append "<name>\t<stdin>\n" as one JSONL row.
	stubBody := "#!/bin/sh\nprintf '%s\\t' \"$4\" >> \"$PDX_TEST_EVENT_CAPTURE\"\ncat >> \"$PDX_TEST_EVENT_CAPTURE\"\nprintf '\\n' >> \"$PDX_TEST_EVENT_CAPTURE\"\n"
	if err := os.WriteFile(stubPath, []byte(stubBody), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	if err := os.Chmod(stubPath, 0o755); err != nil {
		t.Fatalf("chmod stub: %v", err)
	}

	body := renderManagedPlugin(stubPath)
	tail := `
;(async () => {
  const hooks = await PurdexOpenCodeHooks()
  const fire = (ev) => hooks.event({ event: ev })
  await fire({ type: 'session.created', properties: { sessionID: 'parent1', info: { id: 'parent1' } } })
  await fire({ type: 'session.created', properties: { sessionID: 'child1', info: { id: 'child1', parentID: 'parent1' } } })
  await fire({ type: 'session.status', properties: { sessionID: 'child1', status: { type: 'idle' } } })
  await fire({ type: 'session.error', properties: { sessionID: 'child1', error: { name: 'ProviderError', data: { message: 'boom' } } } })
  await fire({ type: 'session.deleted', properties: { sessionID: 'child1', info: { id: 'child1', parentID: 'parent1' } } })
  // Orphan child delete with NO prior created (reload / out-of-order): its
  // own info.parentID must gate it reload-proof, never deleting a frame.
  await fire({ type: 'session.deleted', properties: { sessionID: 'orphan1', info: { id: 'orphan1', parentID: 'parent1' } } })
  await fire({ type: 'session.status', properties: { sessionID: 'parent1', status: { type: 'idle' } } })
  await fire({ type: 'session.deleted', properties: { sessionID: 'parent1' } })
})()
`
	scriptPath := filepath.Join(tmp, "plugin.mjs")
	if err := os.WriteFile(scriptPath, []byte(body+tail), 0o644); err != nil {
		t.Fatalf("write plugin.mjs: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bunPath, scriptPath)
	cmd.Env = append(os.Environ(), "PDX_TEST_EVENT_CAPTURE="+capturePath)
	if output, runErr := cmd.CombinedOutput(); runErr != nil {
		t.Fatalf("unexpected bun failure: err=%v output=%s", runErr, string(output))
	}

	raw, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	var names []string
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if line == "" {
			continue
		}
		name, payloadJSON, found := strings.Cut(line, "\t")
		if !found {
			t.Fatalf("malformed capture row (no tab): %q", line)
		}
		names = append(names, name)
		var payload map[string]any
		if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
			t.Fatalf("unmarshal payload for %q: %v (raw=%q)", name, err, payloadJSON)
		}
		if sid, _ := payload["session_id"].(string); sid != "parent1" {
			t.Fatalf("event %q session_id = %q, want parent1 (no child event may leak)", name, sid)
		}
	}

	want := []string{"PdxSessionStart", "PdxStop", "PdxSessionEnd"}
	if len(names) != len(want) {
		t.Fatalf("captured events = %v, want exactly %v (child lifecycle must be gated)", names, want)
	}
	for i, n := range names {
		if n != want[i] {
			t.Fatalf("captured events = %v, want %v", names, want)
		}
	}
}

// capturedEmit is one row of the JSONL capture the stub pdx writes:
// the event name from argv[4] plus the decoded stdin payload.
type capturedEmit struct {
	Name    string
	Payload map[string]any
}

// requireBun applies the same four skip gates the older runtime tests use
// inline and returns the resolved bun path. Skips (never fails) so a single
// `go test ./...` still works on a host without Bun.
func requireBun(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("opencode plugin runtime is POSIX-only; skipping real-Bun integration test")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skipf("/bin/sh unavailable (%v); skipping real-Bun integration test", err)
	}
	bunPath, err := exec.LookPath("bun")
	if err != nil {
		t.Skip("bun not on PATH; skipping real-Bun integration test")
	}
	if out, err := exec.Command(bunPath, "--version").Output(); err != nil || strings.TrimSpace(string(out)) == "" {
		t.Skipf("bun --version failed (%v); skipping real-Bun integration test", err)
	}
	return bunPath
}

// runRenderedPluginUnderBun renders the managed plugin against a stub pdx
// that appends "<eventName>\t<stdin>\n" per invocation, appends `tail`
// (which must drive the hooks), executes it with `bun` from workDir, and
// returns the captured emits in order.
func runRenderedPluginUnderBun(t *testing.T, bunPath, tail, workDir string) []capturedEmit {
	t.Helper()
	tmp := t.TempDir()
	stubPath := filepath.Join(tmp, "pdx-stub")
	capturePath := filepath.Join(tmp, "events-capture")
	stubBody := "#!/bin/sh\nprintf '%s\\t' \"$4\" >> \"$PDX_TEST_EVENT_CAPTURE\"\ncat >> \"$PDX_TEST_EVENT_CAPTURE\"\nprintf '\\n' >> \"$PDX_TEST_EVENT_CAPTURE\"\n"
	if err := os.WriteFile(stubPath, []byte(stubBody), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	if err := os.Chmod(stubPath, 0o755); err != nil {
		t.Fatalf("chmod stub: %v", err)
	}

	scriptPath := filepath.Join(tmp, "plugin.mjs")
	if err := os.WriteFile(scriptPath, []byte(renderManagedPlugin(stubPath)+tail), 0o644); err != nil {
		t.Fatalf("write plugin.mjs: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bunPath, scriptPath)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "PDX_TEST_EVENT_CAPTURE="+capturePath)
	if output, runErr := cmd.CombinedOutput(); runErr != nil {
		t.Fatalf("unexpected bun failure: err=%v output=%s", runErr, string(output))
	}

	raw, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	var out []capturedEmit
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if line == "" {
			continue
		}
		name, payloadJSON, found := strings.Cut(line, "\t")
		if !found {
			t.Fatalf("malformed capture row (no tab): %q", line)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
			t.Fatalf("unmarshal payload for %q: %v (raw=%q)", name, err, payloadJSON)
		}
		out = append(out, capturedEmit{Name: name, Payload: payload})
	}
	return out
}

// TestRenderManagedPlugin_BunRuntimeEmitsCwd proves at runtime — not by
// source substring — that PdxSessionStart carries the working directory
// the tab-rebuild record needs (spec 2026-09-07 §3.1 / §4.2), that the
// process-cwd fallback works when opencode hands the plugin nothing, and
// that a child session still never produces a second PdxSessionStart.
func TestRenderManagedPlugin_BunRuntimeEmitsCwd(t *testing.T) {
	bunPath := requireBun(t)

	t.Run("directory_from_plugin_input", func(t *testing.T) {
		// opencode calls Plugin(input) with input.directory set; the emitted
		// cwd must be exactly that, not the plugin process cwd.
		const tail = `
;(async () => {
  const hooks = await PurdexOpenCodeHooks({ directory: '/tmp/pdx-project-dir', worktree: '/tmp/pdx-worktree' })
  await hooks.event({ event: { type: 'session.created', properties: { sessionID: 'parent1', info: { id: 'parent1' } } } })
})()
`
		emits := runRenderedPluginUnderBun(t, bunPath, tail, t.TempDir())
		if len(emits) != 1 || emits[0].Name != "PdxSessionStart" {
			t.Fatalf("captured = %+v, want exactly one PdxSessionStart", emits)
		}
		if got, _ := emits[0].Payload["cwd"].(string); got != "/tmp/pdx-project-dir" {
			t.Fatalf("cwd = %q, want %q (payload=%+v)", got, "/tmp/pdx-project-dir", emits[0].Payload)
		}
		if got, _ := emits[0].Payload["session_id"].(string); got != "parent1" {
			t.Fatalf("session_id = %q, want parent1", got)
		}
	})

	t.Run("falls_back_to_process_cwd", func(t *testing.T) {
		// No plugin input at all (older opencode, or a loader that passes
		// nothing): the plugin must still report a usable directory.
		workDir := t.TempDir()
		resolved, err := filepath.EvalSymlinks(workDir)
		if err != nil {
			t.Fatalf("resolve workdir: %v", err)
		}
		const tail = `
;(async () => {
  const hooks = await PurdexOpenCodeHooks()
  await hooks.event({ event: { type: 'session.created', properties: { sessionID: 'parent1', info: { id: 'parent1' } } } })
})()
`
		emits := runRenderedPluginUnderBun(t, bunPath, tail, workDir)
		if len(emits) != 1 || emits[0].Name != "PdxSessionStart" {
			t.Fatalf("captured = %+v, want exactly one PdxSessionStart", emits)
		}
		got, _ := emits[0].Payload["cwd"].(string)
		if got != workDir && got != resolved {
			t.Fatalf("cwd = %q, want process cwd %q (or %q)", got, workDir, resolved)
		}
	})

	t.Run("child_session_emits_subagent_start_not_session_start", func(t *testing.T) {
		// spec §9.3: opencode runs parent and child over the same pane and
		// sender PID, so the plugin-level parentID filter is a precondition
		// of the whole ownership invariant. A subagent must surface as
		// PdxSubagentStart only — a second PdxSessionStart would hijack the
		// parent frame (and would carry a cwd for a session that has none).
		const tail = `
;(async () => {
  const hooks = await PurdexOpenCodeHooks({ directory: '/tmp/pdx-project-dir' })
  await hooks['tool.execute.before'](
    { tool: 'task', callID: 'call1', sessionID: 'parent1' },
    { args: { subagent_type: 'explorer', description: 'dig', prompt: 'go' } },
  )
  await hooks.event({ event: { type: 'session.created', properties: { sessionID: 'child1', info: { id: 'child1', parentID: 'parent1' } } } })
})()
`
		emits := runRenderedPluginUnderBun(t, bunPath, tail, t.TempDir())
		if len(emits) != 1 {
			t.Fatalf("captured = %+v, want exactly one emit (PdxSubagentStart)", emits)
		}
		if emits[0].Name != "PdxSubagentStart" {
			t.Fatalf("captured event = %q, want PdxSubagentStart", emits[0].Name)
		}
		for _, e := range emits {
			if e.Name == "PdxSessionStart" {
				t.Fatalf("child session must never emit PdxSessionStart (payload=%+v)", e.Payload)
			}
		}
		if got, _ := emits[0].Payload["agent_type"].(string); got != "explorer" {
			t.Fatalf("agent_type = %q, want explorer", got)
		}
		if _, hasCwd := emits[0].Payload["cwd"]; hasCwd {
			t.Fatalf("PdxSubagentStart must not carry cwd; payload=%+v", emits[0].Payload)
		}
	})
}

// TestRenderManagedPlugin_BunRuntimeStopAndPromptCarryCwd proves at runtime
// that the two emits spec 2026-09-07 §3.3 measured as cwd-less now carry one.
// It drives a parent session that has a child, so the same run also re-pins
// the parentID child-session filter: spec v1 §9.3 makes that filter a
// precondition of the opencode ownership invariant, and a child event
// leaking through here would attach the wrong session's cwd to the parent's
// frame — exactly the mis-attribution the daemon-side wiring avoids.
func TestRenderManagedPlugin_BunRuntimeStopAndPromptCarryCwd(t *testing.T) {
	bunPath := requireBun(t)

	const projectDir = "/tmp/pdx-project-dir"
	const tail = `
;(async () => {
  const hooks = await PurdexOpenCodeHooks({ directory: '/tmp/pdx-project-dir' })
  const fire = (ev) => hooks.event({ event: ev })
  await fire({ type: 'session.created', properties: { sessionID: 'parent1', info: { id: 'parent1' } } })
  await fire({ type: 'session.created', properties: { sessionID: 'child1', info: { id: 'child1', parentID: 'parent1' } } })
  await hooks['chat.message']({ sessionID: 'parent1', messageID: 'msg1' }, { message: { id: 'msg1' } })
  await hooks['chat.message']({ sessionID: 'child1', messageID: 'msg2' }, { message: { id: 'msg2' } })
  await fire({ type: 'session.status', properties: { sessionID: 'child1', status: { type: 'idle' } } })
  await fire({ type: 'session.status', properties: { sessionID: 'parent1', status: { type: 'idle' } } })
})()
`
	emits := runRenderedPluginUnderBun(t, bunPath, tail, t.TempDir())

	want := []string{"PdxSessionStart", "PdxUserPromptSubmit", "PdxUserPromptSubmit", "PdxStop"}
	var names []string
	for _, e := range emits {
		names = append(names, e.Name)
	}
	// chat.message is not gated by parentID — it is the event that carries
	// the now-current session id when opencode switches sessions in-process
	// (spec §3.3) — so the child's prompt reaches the daemon under the
	// child's own session id. The lifecycle emits (created / idle) stay
	// gated: exactly one PdxSessionStart and one PdxStop, both for parent1.
	if len(names) != len(want) {
		t.Fatalf("captured events = %v, want %v", names, want)
	}
	for i, n := range names {
		if n != want[i] {
			t.Fatalf("captured events = %v, want %v", names, want)
		}
	}

	for _, e := range emits {
		got, _ := e.Payload["cwd"].(string)
		if got != projectDir {
			t.Errorf("%s cwd = %q, want %q (payload=%+v)", e.Name, got, projectDir, e.Payload)
		}
	}
	for _, e := range emits {
		if e.Name == "PdxUserPromptSubmit" {
			continue
		}
		if sid, _ := e.Payload["session_id"].(string); sid != "parent1" {
			t.Errorf("%s session_id = %q, want parent1 (the parentID filter must gate child lifecycle)", e.Name, sid)
		}
	}
}
