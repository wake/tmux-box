package opencode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// payloadFixturesDir is the relative path from the package directory to
// the per-event payload fixture set. The directory name is versioned to
// the audited upstream tag so future audits never overwrite historical
// fixtures (plan v1.3 §2.4).
const payloadFixturesDir = "testdata/opencode-1.14.23-payloads"

// fixtureEnvelope is the JSON shape of every payload fixture in
// payloadFixturesDir. Kind discriminates Bus events (use EventType +
// Properties) from strong hooks (use HookName + Input + Output) so OC1
// and OC1a can dispatch through a single loader.
type fixtureEnvelope struct {
	Kind       string                 `json:"kind"`
	EventType  string                 `json:"eventType,omitempty"`
	HookName   string                 `json:"hookName,omitempty"`
	Properties map[string]interface{} `json:"properties,omitempty"`
	Input      map[string]interface{} `json:"input,omitempty"`
	Output     map[string]interface{} `json:"output,omitempty"`
}

func loadPayloadFixture(t *testing.T, name string) fixtureEnvelope {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(payloadFixturesDir, name))
	if err != nil {
		t.Fatalf("load fixture %q: %v", name, err)
	}
	var env fixtureEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("parse fixture %q: %v", name, err)
	}
	return env
}

// pluginSimState is a Go-side simulator for the JS callbacks rendered by
// renderManagedPlugin. It mirrors the production JS at the payload-shape
// level — intentionally distinct from the simpler pluginState used by
// the legacy state-machine tests (which suppresses idle via a single
// bool rather than a sessionID set). Production NEVER calls this; OC1
// uses it to verify the JS template would emit a given (Name, Payload)
// for a given fixture.
type pluginSimState struct {
	activeSubagents        map[string]string
	suppressIdleForSession map[string]bool
	// subagentSessions mirrors the JS template's Map keyed
	// childSessionID → parentSessionID. Learned from session.created
	// (info.parentID set) and used to gate every parent-level lifecycle
	// emit for a child (subagent) session. See spec 2026-07-19.
	subagentSessions map[string]string
}

func newPluginSimState() *pluginSimState {
	return &pluginSimState{
		activeSubagents:        make(map[string]string),
		suppressIdleForSession: make(map[string]bool),
		subagentSessions:       make(map[string]string),
	}
}

// simulateBusEvent dispatches a Bus event payload through the equivalent
// of renderManagedPlugin's `event` switch.
func (s *pluginSimState) simulateBusEvent(eventType string, properties map[string]any) (mappedHookEvent, bool) {
	switch eventType {
	case "session.created":
		sid := resolveSid(properties)
		parentID := ""
		if info, ok := properties["info"].(map[string]any); ok {
			parentID = strMapVal(info, "parentID")
		}
		if parentID != "" {
			// Suppress the child start; only key a non-empty sid so an empty
			// key can never swallow an unrelated empty-sid parent delete.
			if sid != "" {
				s.subagentSessions[sid] = parentID
			}
			return mappedHookEvent{}, false
		}
		return mappedHookEvent{
			Name: "PdxSessionStart",
			Payload: map[string]any{
				"session_id": sid,
			},
		}, true
	case "permission.asked":
		return mappedHookEvent{
			Name: "PdxPermissionRequest",
			Payload: map[string]any{
				"request_type": "permission",
				"permission":   properties["permission"],
				"patterns":     properties["patterns"],
			},
		}, true
	case "question.asked":
		return mappedHookEvent{
			Name: "PdxPermissionRequest",
			Payload: map[string]any{
				"request_type": "question",
				"questions":    properties["questions"],
			},
		}, true
	case "session.error":
		sid := resolveSid(properties)
		if sid != "" {
			if _, isChild := s.subagentSessions[sid]; isChild {
				return mappedHookEvent{}, false
			}
			s.suppressIdleForSession[sid] = true
		}
		var errName, errDetails string
		if errObj, ok := properties["error"].(map[string]any); ok {
			errName = strMapVal(errObj, "name")
			if dataObj, ok := errObj["data"].(map[string]any); ok {
				errDetails = strMapVal(dataObj, "message")
			}
		}
		return mappedHookEvent{
			Name: "PdxStopFailure",
			Payload: map[string]any{
				"error":         errName,
				"error_details": errDetails,
			},
		}, true
	case "session.status":
		// Decision 3 switch: subscribe session.status, filter to {type:"idle"}.
		// Decision 4 defer: busy/retry variants no-op; trigger conditions
		// for adoption are tracked in follow-up issue #661.
		statusObj, _ := properties["status"].(map[string]any)
		if strMapVal(statusObj, "type") != "idle" {
			return mappedHookEvent{}, false
		}
		sid := resolveSid(properties)
		if sid != "" {
			if _, isChild := s.subagentSessions[sid]; isChild {
				return mappedHookEvent{}, false
			}
		}
		if s.suppressIdleForSession[sid] {
			delete(s.suppressIdleForSession, sid)
			return mappedHookEvent{}, false
		}
		return mappedHookEvent{
			Name: "PdxStop",
			Payload: map[string]any{
				"session_id": sid,
			},
		}, true
	case "session.deleted":
		sid := resolveSid(properties)
		parentID := ""
		if info, ok := properties["info"].(map[string]any); ok {
			parentID = strMapVal(info, "parentID")
		}
		if parentID != "" {
			// Child delete: gate on the event's own parentID, not the map —
			// reload-proof, so a child delete never emits PdxSessionEnd
			// against the parent frame even if we never saw its created.
			if sid != "" {
				delete(s.subagentSessions, sid)
			}
			return mappedHookEvent{}, false
		}
		for childID, pid := range s.subagentSessions {
			if pid == sid {
				delete(s.subagentSessions, childID)
			}
		}
		return mappedHookEvent{
			Name: "PdxSessionEnd",
			Payload: map[string]any{
				"session_id": sid,
			},
		}, true
	}
	return mappedHookEvent{}, false
}

// simulateStrongHook dispatches a strong-hook fixture through the
// equivalent of renderManagedPlugin's top-level callback for that hook.
func (s *pluginSimState) simulateStrongHook(hookName string, input, output map[string]any) (mappedHookEvent, bool) {
	switch hookName {
	case "chat.message":
		return s.simulateChatMessage(input, output), true
	case "tool.execute.before":
		return s.simulateToolExecuteBefore(input, output)
	case "tool.execute.after":
		return s.simulateToolExecuteAfter(input, output)
	}
	return mappedHookEvent{}, false
}

func (s *pluginSimState) simulateChatMessage(input, output map[string]any) mappedHookEvent {
	sessionID := strMapVal(input, "sessionID")
	// New prompt cycle: clear any stale suppressIdleForSession entry the
	// previous cycle's session.error armed. Without this, an idle that
	// belongs to the new cycle would be consumed by the stale entry and
	// Stop would never fire. Mirror of the JS template change.
	if sessionID != "" {
		delete(s.suppressIdleForSession, sessionID)
	}
	messageID := strMapVal(input, "messageID")
	if messageID == "" {
		if msg, ok := output["message"].(map[string]any); ok {
			messageID = strMapVal(msg, "id")
		}
	}
	var agentName string
	if msg, ok := output["message"].(map[string]any); ok {
		agentName = strMapVal(msg, "agent")
	}
	if agentName == "" {
		agentName = strMapVal(input, "agent")
	}
	var modelName string
	if model, ok := input["model"].(map[string]any); ok {
		provID := strMapVal(model, "providerID")
		modelID := strMapVal(model, "modelID")
		modelName = provID + "/" + modelID
	}
	payload := map[string]any{
		"message_id": messageID,
		"agent":      agentName,
		"modelName":  modelName,
		"source":     "chat.message",
	}
	// Mirror of the JS template's child-prompt gate: parent and child share
	// one tmux pane and one sender PID, so a child's session_id would be
	// written onto the parent's frame. The event itself still goes out
	// unchanged in every other field — only session_id is dropped, exactly
	// as the JS sends `session_id: undefined` (which JSON omits).
	//
	// `cwd` is not modelled by this simulator at all and stays unmodelled;
	// that is a pre-existing approximation, not part of this gate.
	_, isChild := s.subagentSessions[sessionID]
	if !(sessionID != "" && isChild) {
		payload["session_id"] = sessionID
	}
	return mappedHookEvent{
		Name:    "PdxUserPromptSubmit",
		Payload: payload,
	}
}

func (s *pluginSimState) simulateToolExecuteBefore(input, output map[string]any) (mappedHookEvent, bool) {
	if strMapVal(input, "tool") != "task" || strMapVal(input, "callID") == "" {
		return mappedHookEvent{}, false
	}
	sessionID := strMapVal(input, "sessionID")
	callID := strMapVal(input, "callID")
	key := sessionID + ":" + callID
	if _, exists := s.activeSubagents[key]; exists {
		return mappedHookEvent{}, false
	}
	args, _ := output["args"].(map[string]any)
	agentType := simAgentTypeFromArgs(args)
	s.activeSubagents[key] = agentType
	return mappedHookEvent{
		Name: "PdxSubagentStart",
		Payload: map[string]any{
			"agent_id":    callID,
			"agent_type":  agentType,
			"description": strMapVal(args, "description"),
			"prompt":      strMapVal(args, "prompt"),
		},
	}, true
}

func (s *pluginSimState) simulateToolExecuteAfter(input, output map[string]any) (mappedHookEvent, bool) {
	if strMapVal(input, "tool") != "task" || strMapVal(input, "callID") == "" {
		return mappedHookEvent{}, false
	}
	sessionID := strMapVal(input, "sessionID")
	callID := strMapVal(input, "callID")
	key := sessionID + ":" + callID
	agentType, exists := s.activeSubagents[key]
	if !exists {
		return mappedHookEvent{}, false
	}
	delete(s.activeSubagents, key)
	return mappedHookEvent{
		Name: "PdxSubagentStop",
		Payload: map[string]any{
			"agent_id":   callID,
			"agent_type": agentType,
			"title":      strMapVal(output, "title"),
			"output":     strMapVal(output, "output"),
		},
	}, true
}

// resolveSid mirrors the JS template's defensive session-id resolution:
// prefer properties.sessionID, fall back to properties.info.id, else empty.
// The generated SDK type lists only info for session.created/deleted, so the
// top-level sessionID is not guaranteed across versions.
func resolveSid(properties map[string]any) string {
	if sid := strMapVal(properties, "sessionID"); sid != "" {
		return sid
	}
	if info, ok := properties["info"].(map[string]any); ok {
		return strMapVal(info, "id")
	}
	return ""
}

func simAgentTypeFromArgs(args map[string]any) string {
	if t := strMapVal(args, "subagent_type"); t != "" {
		return t
	}
	if t := strMapVal(args, "agent"); t != "" {
		return t
	}
	return "task"
}

// pathMustExist resolves a dotted path through nested map[string]any and
// returns ok=false if any segment is missing or types mismatch. OC1a uses
// it to assert the fixture covers every field the JS plugin reads.
func pathMustExist(envelope map[string]any, dotted string) (any, bool) {
	parts := strings.Split(dotted, ".")
	var cur any = envelope
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, exists := m[p]
		if !exists {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

// envelopeAsMap exposes the fixtureEnvelope as a generic map[string]any
// so pathMustExist walks Bus events and strong hooks the same way.
func envelopeAsMap(env fixtureEnvelope) map[string]any {
	switch env.Kind {
	case "bus-event":
		return map[string]any{"properties": map[string]any(env.Properties)}
	case "strong-hook":
		return map[string]any{
			"input":  map[string]any(env.Input),
			"output": map[string]any(env.Output),
		}
	}
	return nil
}

// pluginEventContract declares what every plugin-subscribed event needs
// in its fixture. RequiredFields is the minimum surface OC1a guarantees;
// extra fields in fixtures are fine.
type pluginEventContract struct {
	Event          string
	FixtureFile    string
	Kind           string
	RequiredFields []string
}

// pluginContracts is the post-Commit-5 event-to-handler map. The
// `session.status` entry is the Decision 3 switch target (filtered to
// {type:"idle"} inside the simulator); pre-switch sessions used a bare
// `session.idle` Bus event, deprecated upstream.
func pluginContracts() []pluginEventContract {
	return []pluginEventContract{
		{
			Event:          "session.created",
			FixtureFile:    "session.created.json",
			Kind:           "bus-event",
			RequiredFields: []string{"properties.sessionID"},
		},
		{
			Event:          "permission.asked",
			FixtureFile:    "permission.asked.json",
			Kind:           "bus-event",
			RequiredFields: []string{"properties.permission", "properties.patterns"},
		},
		{
			Event:          "question.asked",
			FixtureFile:    "question.asked.json",
			Kind:           "bus-event",
			RequiredFields: []string{"properties.questions"},
		},
		{
			Event:       "session.error",
			FixtureFile: "session.error.json",
			Kind:        "bus-event",
			RequiredFields: []string{
				"properties.sessionID",
				"properties.error.name",
				"properties.error.data.message",
			},
		},
		{
			Event:          "session.status",
			FixtureFile:    "session.status.json",
			Kind:           "bus-event",
			RequiredFields: []string{"properties.sessionID", "properties.status.type"},
		},
		{
			Event:          "session.deleted",
			FixtureFile:    "session.deleted.json",
			Kind:           "bus-event",
			RequiredFields: []string{"properties.sessionID"},
		},
		{
			Event:       "chat.message",
			FixtureFile: "chat.message.json",
			Kind:        "strong-hook",
			RequiredFields: []string{
				"input.sessionID",
				"input.messageID",
				"input.agent",
				"input.model.providerID",
				"input.model.modelID",
				"output.message.id",
				"output.message.agent",
			},
		},
		{
			Event:       "tool.execute.before",
			FixtureFile: "tool.execute.before.json",
			Kind:        "strong-hook",
			RequiredFields: []string{
				"input.tool",
				"input.callID",
				"input.sessionID",
				"output.args.subagent_type",
				"output.args.description",
				"output.args.prompt",
			},
		},
		{
			Event:       "tool.execute.after",
			FixtureFile: "tool.execute.after.json",
			Kind:        "strong-hook",
			RequiredFields: []string{
				"input.tool",
				"input.callID",
				"input.sessionID",
				"output.title",
				"output.output",
			},
		},
	}
}

// TestOpenCodeTemplateEventContractsDocumented (plan v1.3 §3 OC1a). For
// every plugin-subscribed event the JS plugin reads from properties /
// input / output, the corresponding fixture must declare a field at the
// same dotted path. This is the bidirectional contract guard between
// renderManagedPlugin and the testdata/opencode-1.14.23-payloads/ set.
func TestOpenCodeTemplateEventContractsDocumented(t *testing.T) {
	for _, c := range pluginContracts() {
		c := c
		t.Run(c.Event, func(t *testing.T) {
			env := loadPayloadFixture(t, c.FixtureFile)
			if env.Kind != c.Kind {
				t.Fatalf("fixture kind = %q, want %q", env.Kind, c.Kind)
			}
			switch c.Kind {
			case "bus-event":
				if env.EventType != c.Event {
					t.Fatalf("fixture eventType = %q, want %q", env.EventType, c.Event)
				}
			case "strong-hook":
				if env.HookName != c.Event {
					t.Fatalf("fixture hookName = %q, want %q", env.HookName, c.Event)
				}
			}
			payload := envelopeAsMap(env)
			for _, p := range c.RequiredFields {
				v, ok := pathMustExist(payload, p)
				if !ok {
					t.Errorf("required field %q missing from fixture %q", p, c.FixtureFile)
					continue
				}
				if isZeroValue(v) {
					t.Errorf("required field %q is zero/empty in fixture %q", p, c.FixtureFile)
				}
			}
		})
	}
}

// isZeroValue is a permissive zero check for fixture-loaded values: empty
// string / nil / empty slice / empty map all count. Numbers and bools are
// never zero (false / 0 may be valid payload values).
func isZeroValue(v any) bool {
	if v == nil {
		return true
	}
	switch x := v.(type) {
	case string:
		return x == ""
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

// TestOpenCodePluginTemplate_UsesVerifiedEvents (plan v1.3 §3 OC1). For
// each plugin-subscribed event, load the fixture, run the JS-mirrored
// simulator, and assert the (Name, Payload) the JS plugin would emit().
// Unconditional (Round 1 C1).
//
// Verifies the post-Decision-3 mapping where `session.status` filtered to
// {type:"idle"} drives Stop; busy/retry variants are received-but-no-op
// (Decision 4 defer).
func TestOpenCodePluginTemplate_UsesVerifiedEvents(t *testing.T) {
	cases := []struct {
		event         string
		fixtureFile   string
		kind          string
		setup         func(*pluginSimState, fixtureEnvelope)
		expectName    string
		expectPayload map[string]any
	}{
		{
			event:       "session.created",
			fixtureFile: "session.created.json",
			kind:        "bus-event",
			expectName:  "PdxSessionStart",
			expectPayload: map[string]any{
				"session_id": "ses_fixture_session_created_001",
			},
		},
		{
			event:       "permission.asked",
			fixtureFile: "permission.asked.json",
			kind:        "bus-event",
			expectName:  "PdxPermissionRequest",
			expectPayload: map[string]any{
				"request_type": "permission",
				"permission":   "edit",
				"patterns":     []any{"src/**/*.ts"},
			},
		},
		{
			event:       "question.asked",
			fixtureFile: "question.asked.json",
			kind:        "bus-event",
			expectName:  "PdxPermissionRequest",
			expectPayload: map[string]any{
				"request_type": "question",
				"questions": []any{
					map[string]any{"label": "approve?", "value": "approve"},
					map[string]any{"label": "deny?", "value": "deny"},
				},
			},
		},
		{
			event:       "session.error",
			fixtureFile: "session.error.json",
			kind:        "bus-event",
			expectName:  "PdxStopFailure",
			expectPayload: map[string]any{
				"error":         "ProviderError",
				"error_details": "request timed out",
			},
		},
		{
			event:       "session.status",
			fixtureFile: "session.status.json",
			kind:        "bus-event",
			expectName:  "PdxStop",
			expectPayload: map[string]any{
				"session_id": "ses_fixture_status_idle_001",
			},
		},
		{
			event:       "session.deleted",
			fixtureFile: "session.deleted.json",
			kind:        "bus-event",
			expectName:  "PdxSessionEnd",
			expectPayload: map[string]any{
				"session_id": "ses_fixture_deleted_001",
			},
		},
		{
			event:       "chat.message",
			fixtureFile: "chat.message.json",
			kind:        "strong-hook",
			expectName:  "PdxUserPromptSubmit",
			expectPayload: map[string]any{
				"session_id": "ses_fixture_chat_001",
				"message_id": "msg_fixture_chat_001",
				"agent":      "build",
				"modelName":  "anthropic/claude-3-5-sonnet",
				"source":     "chat.message",
			},
		},
		{
			event:       "tool.execute.before",
			fixtureFile: "tool.execute.before.json",
			kind:        "strong-hook",
			expectName:  "PdxSubagentStart",
			expectPayload: map[string]any{
				"agent_id":    "call_fixture_task_001",
				"agent_type":  "Explore",
				"description": "explore process tree",
				"prompt":      "find tmux sessions",
			},
		},
		{
			event:       "tool.execute.after",
			fixtureFile: "tool.execute.after.json",
			kind:        "strong-hook",
			setup: func(s *pluginSimState, env fixtureEnvelope) {
				key := strMapVal(env.Input, "sessionID") + ":" + strMapVal(env.Input, "callID")
				s.activeSubagents[key] = "Explore"
			},
			expectName: "PdxSubagentStop",
			expectPayload: map[string]any{
				"agent_id":   "call_fixture_task_001",
				"agent_type": "Explore",
				"title":      "explore complete",
				"output":     "found 3 sessions",
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.event, func(t *testing.T) {
			env := loadPayloadFixture(t, tc.fixtureFile)
			state := newPluginSimState()
			if tc.setup != nil {
				tc.setup(state, env)
			}
			var (
				got mappedHookEvent
				ok  bool
			)
			switch tc.kind {
			case "bus-event":
				got, ok = state.simulateBusEvent(env.EventType, env.Properties)
			case "strong-hook":
				got, ok = state.simulateStrongHook(env.HookName, env.Input, env.Output)
			default:
				t.Fatalf("unknown kind %q", tc.kind)
			}
			if !ok {
				t.Fatalf("simulator did not emit for %q", tc.event)
			}
			if got.Name != tc.expectName {
				t.Fatalf("event name = %q, want %q", got.Name, tc.expectName)
			}
			if !reflect.DeepEqual(got.Payload, tc.expectPayload) {
				t.Fatalf("payload mismatch for %q\n  got:  %s\n  want: %s",
					tc.event, prettyJSON(got.Payload), prettyJSON(tc.expectPayload))
			}
		})
	}
}

func prettyJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%#v", v)
	}
	return string(b)
}

// TestOpenCodePluginTemplate_ChatMessageClearsStaleErrorSuppression covers
// the cross-cycle leak where a session.error armed suppressIdleForSession
// for sessionID S, but the next idle never came (e.g. user submitted a new
// prompt before the runtime emitted a status event, or session.error →
// session.deleted → user reused the same sessionID). Without the new
// chat.message clear, the next legitimate idle was consumed by the stale
// entry and Stop was never emitted — the session stuck visually as
// Running/Error.
//
// Behavior expected: chat.message for sessionID S clears the stale entry
// so the subsequent session.status idle emits Stop normally.
func TestOpenCodePluginTemplate_ChatMessageClearsStaleErrorSuppression(t *testing.T) {
	state := newPluginSimState()

	// Step 1: session.error arms suppression for sessionID "S".
	if _, ok := state.simulateBusEvent("session.error", map[string]any{
		"sessionID": "S",
		"error": map[string]any{
			"name": "ProviderError",
			"data": map[string]any{"message": "boom"},
		},
	}); !ok {
		t.Fatal("session.error must emit PdxStopFailure")
	}
	if !state.suppressIdleForSession["S"] {
		t.Fatal("suppressIdleForSession[S] should be armed after session.error")
	}

	// Step 2: chat.message for the same sessionID begins a new prompt
	// cycle and must clear the stale suppression.
	state.simulateChatMessage(
		map[string]any{"sessionID": "S", "messageID": "msg_1"},
		map[string]any{"message": map[string]any{"id": "msg_1", "agent": "build"}},
	)
	if state.suppressIdleForSession["S"] {
		t.Fatal("chat.message should clear stale suppressIdleForSession[S]")
	}

	// Step 3: session.status idle should now emit Stop, not be swallowed.
	got, ok := state.simulateBusEvent("session.status", map[string]any{
		"sessionID": "S",
		"status":    map[string]any{"type": "idle"},
	})
	if !ok {
		t.Fatal("idle after chat.message clear should emit Stop")
	}
	if got.Name != "PdxStop" {
		t.Fatalf("event name = %q, want PdxStop", got.Name)
	}
	if sid := strMapVal(got.Payload, "session_id"); sid != "S" {
		t.Fatalf("session_id = %q, want %q", sid, "S")
	}
}

// TestOpenCodePluginTemplate_StaleSuppressionWithoutChatMessage proves the
// boundary: when no chat.message intervenes between session.error and the
// next session.status idle, the original suppression contract is
// preserved (idle is consumed by the suppression, no Stop emitted). This
// guards against over-clearing in the chat.message handler.
func TestOpenCodePluginTemplate_StaleSuppressionWithoutChatMessage(t *testing.T) {
	state := newPluginSimState()

	if _, ok := state.simulateBusEvent("session.error", map[string]any{
		"sessionID": "S",
		"error": map[string]any{
			"name": "ProviderError",
			"data": map[string]any{"message": "boom"},
		},
	}); !ok {
		t.Fatal("session.error must emit PdxStopFailure")
	}

	// No chat.message between error and idle. The first idle should be
	// suppressed (existing behavior preserved).
	if _, ok := state.simulateBusEvent("session.status", map[string]any{
		"sessionID": "S",
		"status":    map[string]any{"type": "idle"},
	}); ok {
		t.Fatal("idle immediately after session.error must be suppressed")
	}
	if state.suppressIdleForSession["S"] {
		t.Fatal("suppression should be consumed by the suppressed idle")
	}
}

// caseKeyPattern captures Bus event keys from the rendered template's
// `event` switch (e.g. `case 'session.created':`). The simulator-only
// OC1 path runs against pluginSimState and never reads the rendered JS,
// so a silent typo or removed case would not surface there. This static
// check closes that gap (full Bun-runtime parity is tracked separately).
var caseKeyPattern = regexp.MustCompile(`case\s+'([\w.]+)'\s*:`)

// strongHookKeyPattern captures top-level strong-hook callback keys from
// the rendered template (e.g. `'chat.message': async (input, output) =>`).
var strongHookKeyPattern = regexp.MustCompile(`'([\w.]+)'\s*:\s*async\s*\(input`)

func extractCaseKeys(body string) []string {
	matches := caseKeyPattern.FindAllStringSubmatch(body, -1)
	seen := make(map[string]bool, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) < 2 || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		out = append(out, m[1])
	}
	sort.Strings(out)
	return out
}

func extractStrongHookKeys(body string) []string {
	matches := strongHookKeyPattern.FindAllStringSubmatch(body, -1)
	seen := make(map[string]bool, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) < 2 || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		out = append(out, m[1])
	}
	sort.Strings(out)
	return out
}

// TestOpenCodePluginTemplate_RenderedTemplateMatchesContracts statically
// inspects the JS body returned by renderManagedPlugin and asserts:
//   - every Bus event in pluginContracts() (Kind="bus-event") appears as
//     a `case 'X':` in the event switch, and vice versa
//   - every strong hook in pluginContracts() (Kind="strong-hook") appears
//     as a top-level `'X': async (input, output) =>` callback, and vice
//     versa
//
// OC1 verifies pluginSimState (a Go mirror of the JS), not the rendered
// JS itself. If renderManagedPlugin's template silently dropped a case
// or reverted a key (e.g. session.status -> session.idle), OC1 would
// stay green because it never reads the rendered string. This test
// closes that simulator-vs-template gap statically. Full Bun-runtime
// parity is tracked in a separate follow-up issue.
func TestOpenCodePluginTemplate_RenderedTemplateMatchesContracts(t *testing.T) {
	body := renderManagedPlugin("/fake/pdx")

	var expectedBus, expectedHooks []string
	for _, c := range pluginContracts() {
		switch c.Kind {
		case "bus-event":
			expectedBus = append(expectedBus, c.Event)
		case "strong-hook":
			expectedHooks = append(expectedHooks, c.Event)
		}
	}
	sort.Strings(expectedBus)
	sort.Strings(expectedHooks)

	gotBus := extractCaseKeys(body)
	if !reflect.DeepEqual(gotBus, expectedBus) {
		t.Errorf("rendered case keys mismatch\n  got:  %v\n  want: %v", gotBus, expectedBus)
	}

	gotHooks := extractStrongHookKeys(body)
	if !reflect.DeepEqual(gotHooks, expectedHooks) {
		t.Errorf("rendered strong-hook keys mismatch\n  got:  %v\n  want: %v", gotHooks, expectedHooks)
	}
}

// childCreatedProps builds a session.created bus-event `properties` map
// for a subagent (child) session: top-level sessionID plus info.id and
// info.parentID (the authoritative child signal).
func childCreatedProps(childID, parentID string) map[string]any {
	return map[string]any{
		"sessionID": childID,
		"info": map[string]any{
			"id":       childID,
			"parentID": parentID,
		},
	}
}

// childDeletedProps builds a session.deleted bus-event `properties` map
// for a subagent (child) session. Real opencode publishes full info on
// session.deleted (session.ts:624), so the child delete carries its own
// info.parentID — the authoritative, reload-proof child signal.
func childDeletedProps(childID, parentID string) map[string]any {
	return map[string]any{
		"sessionID": childID,
		"info": map[string]any{
			"id":       childID,
			"parentID": parentID,
		},
	}
}

// idleProps builds a session.status idle bus-event `properties` map.
func idleProps(sessionID string) map[string]any {
	return map[string]any{
		"sessionID": sessionID,
		"status":    map[string]any{"type": "idle"},
	}
}

// errorProps builds a session.error bus-event `properties` map.
func errorProps(sessionID string) map[string]any {
	return map[string]any{
		"sessionID": sessionID,
		"error": map[string]any{
			"name": "ProviderError",
			"data": map[string]any{"message": "boom"},
		},
	}
}

// TestOpenCodePluginTemplate_ChildSessionGating covers the L1 fix: a
// subagent (Task tool) runs as a child opencode session with its own full
// lifecycle. The plugin must gate every parent-level Pdx* emit for a known
// child, learning child→parent from session.created's info.parentID. The
// subagent's real representation is already carried by
// PdxSubagentStart/Stop (tool.execute.before/after) — unchanged here.
func TestOpenCodePluginTemplate_ChildSessionGating(t *testing.T) {
	t.Run("child_created_registers_and_no_emit", func(t *testing.T) {
		s := newPluginSimState()
		if _, ok := s.simulateBusEvent("session.created", childCreatedProps("child1", "parent1")); ok {
			t.Fatal("child session.created must not emit PdxSessionStart")
		}
		if s.subagentSessions["child1"] != "parent1" {
			t.Fatalf("child1 should be registered under parent1; map=%v", s.subagentSessions)
		}
	})

	t.Run("parent_created_emits_and_not_registered", func(t *testing.T) {
		s := newPluginSimState()
		got, ok := s.simulateBusEvent("session.created", map[string]any{
			"sessionID": "parent1",
			"info":      map[string]any{"id": "parent1"},
		})
		if !ok || got.Name != "PdxSessionStart" {
			t.Fatalf("parent session.created must emit PdxSessionStart; ok=%v name=%q", ok, got.Name)
		}
		if _, exists := s.subagentSessions["parent1"]; exists {
			t.Fatal("parent must not be registered as a subagent session")
		}
	})

	t.Run("child_created_fallback_to_info_id", func(t *testing.T) {
		s := newPluginSimState()
		// No top-level sessionID: only info.id + info.parentID. Proves the
		// `sessionID || info.id` fallback registers under info.id.
		if _, ok := s.simulateBusEvent("session.created", map[string]any{
			"info": map[string]any{"id": "child2", "parentID": "parentX"},
		}); ok {
			t.Fatal("child session.created (no top-level sessionID) must not emit")
		}
		if s.subagentSessions["child2"] != "parentX" {
			t.Fatalf("child2 must be registered under info.id fallback; map=%v", s.subagentSessions)
		}
	})

	t.Run("child_idle_suppressed_parent_idle_emits", func(t *testing.T) {
		s := newPluginSimState()
		s.simulateBusEvent("session.created", childCreatedProps("child1", "parent1"))
		if _, ok := s.simulateBusEvent("session.status", idleProps("child1")); ok {
			t.Fatal("registered child idle must not emit PdxStop")
		}
		got, ok := s.simulateBusEvent("session.status", idleProps("parent1"))
		if !ok || got.Name != "PdxStop" {
			t.Fatalf("parent idle must emit PdxStop; ok=%v name=%q", ok, got.Name)
		}
	})

	t.Run("child_error_gated_and_suppress_not_armed", func(t *testing.T) {
		s := newPluginSimState()
		s.simulateBusEvent("session.created", childCreatedProps("child1", "parent1"))
		if _, ok := s.simulateBusEvent("session.error", errorProps("child1")); ok {
			t.Fatal("registered child error must not emit PdxStopFailure")
		}
		// White-box: child error must NOT arm suppressIdleForSession — else a
		// child-armed entry that no child idle ever clears would leak.
		if s.suppressIdleForSession["child1"] {
			t.Fatal("child error must NOT arm suppressIdleForSession[child1]")
		}
	})

	t.Run("child_deleted_gated_and_removed", func(t *testing.T) {
		s := newPluginSimState()
		s.simulateBusEvent("session.created", childCreatedProps("child1", "parent1"))
		if _, ok := s.simulateBusEvent("session.deleted", childDeletedProps("child1", "parent1")); ok {
			t.Fatal("registered child deleted must not emit PdxSessionEnd")
		}
		if _, exists := s.subagentSessions["child1"]; exists {
			t.Fatal("child must be removed from subagentSessions after session.deleted")
		}
	})

	t.Run("parent_deleted_emits_and_prunes_only_its_children", func(t *testing.T) {
		s := newPluginSimState()
		s.simulateBusEvent("session.created", childCreatedProps("child1", "parent1"))
		s.simulateBusEvent("session.created", childCreatedProps("child2", "parent1"))
		got, ok := s.simulateBusEvent("session.deleted", map[string]any{"sessionID": "parent1"})
		if !ok || got.Name != "PdxSessionEnd" {
			t.Fatalf("parent deleted must emit PdxSessionEnd; ok=%v name=%q", ok, got.Name)
		}
		if len(s.subagentSessions) != 0 {
			t.Fatalf("parent1's children must all be pruned; map=%v", s.subagentSessions)
		}
	})

	t.Run("sibling_parents_scoped_cleanup", func(t *testing.T) {
		s := newPluginSimState()
		s.simulateBusEvent("session.created", childCreatedProps("childA", "parentA"))
		s.simulateBusEvent("session.created", childCreatedProps("childB", "parentB"))
		s.simulateBusEvent("session.deleted", map[string]any{"sessionID": "parentA"})
		if _, exists := s.subagentSessions["childA"]; exists {
			t.Fatal("childA must be pruned when parentA is deleted")
		}
		if s.subagentSessions["childB"] != "parentB" {
			t.Fatalf("childB must remain registered after parentA delete; map=%v", s.subagentSessions)
		}
		if _, ok := s.simulateBusEvent("session.status", idleProps("childB")); ok {
			t.Fatal("childB idle must still be suppressed after sibling parentA deleted")
		}
	})

	t.Run("multi_subagent_idles_suppressed_parent_stops_once", func(t *testing.T) {
		s := newPluginSimState()
		children := []string{"c1", "c2", "c3"}
		for _, c := range children {
			s.simulateBusEvent("session.created", childCreatedProps(c, "parent1"))
		}
		for _, c := range children {
			if _, ok := s.simulateBusEvent("session.status", idleProps(c)); ok {
				t.Fatalf("child %s idle must be suppressed", c)
			}
		}
		got, ok := s.simulateBusEvent("session.status", idleProps("parent1"))
		if !ok || got.Name != "PdxStop" {
			t.Fatalf("parent idle must emit PdxStop; ok=%v name=%q", ok, got.Name)
		}
	})

	t.Run("full_sequence_only_parent_stop_and_structures_clean", func(t *testing.T) {
		s := newPluginSimState()
		s.simulateBusEvent("session.created", childCreatedProps("child1", "parent1"))
		if _, ok := s.simulateBusEvent("session.status", idleProps("child1")); ok {
			t.Fatal("child idle must be suppressed")
		}
		if _, ok := s.simulateBusEvent("session.error", errorProps("child1")); ok {
			t.Fatal("child error must be gated")
		}
		if _, ok := s.simulateBusEvent("session.deleted", childDeletedProps("child1", "parent1")); ok {
			t.Fatal("child deleted must be gated")
		}
		got, ok := s.simulateBusEvent("session.status", idleProps("parent1"))
		if !ok || got.Name != "PdxStop" {
			t.Fatalf("parent idle must emit PdxStop; ok=%v name=%q", ok, got.Name)
		}
		if len(s.subagentSessions) != 0 {
			t.Fatalf("subagentSessions must be clean after sequence; map=%v", s.subagentSessions)
		}
		if len(s.suppressIdleForSession) != 0 {
			t.Fatalf("suppressIdleForSession must be clean after sequence; map=%v", s.suppressIdleForSession)
		}
	})
}

// TestOpenCodePluginTemplate_ChildSessionGatingReloadWindow covers the
// reload / out-of-order window (spec Design decision 4). The map is only
// populated after a child's session.created; if the plugin reloads while a
// subagent is mid-flight (or an event arrives before that child's created)
// the map is empty for it. session.deleted publishes full info
// (session.ts:624) so it is gated on the event's own info.parentID — NOT
// the map — which is reload-proof and removes the worst failure (a child
// delete deleting the PARENT frame). session.status idle / session.error
// genuinely lack parentID (#30043; SDK EventSessionError = {sessionID?,
// error?}) so they stay map-based; an unseen child's idle/error still
// emits — a recoverable, documented limitation asserted here as the
// current behavior (not a pretend fix). SDK-fetch hardening is follow-up.
func TestOpenCodePluginTemplate_ChildSessionGatingReloadWindow(t *testing.T) {
	t.Run("orphan_child_deleted_gated_via_event_parentID", func(t *testing.T) {
		s := newPluginSimState()
		// No prior session.created — map is empty for childX. The delete
		// event carries its own parentID, so it must still be gated.
		if _, ok := s.simulateBusEvent("session.deleted", map[string]any{
			"sessionID": "childX",
			"info":      map[string]any{"id": "childX", "parentID": "parent1"},
		}); ok {
			t.Fatal("orphan child session.deleted (event parentID set) must be gated even with empty map")
		}
	})

	t.Run("orphan_child_idle_and_error_still_emit_documented_limitation", func(t *testing.T) {
		// Reload window: idle/error lack parentID, so an unseen child's
		// idle/error still emits (map-based). This is the recoverable,
		// documented limitation (idle derives notification_silent and
		// self-corrects on the parent's next event). Asserted as CURRENT
		// behavior — not a pretend fix. SDK-fetch hardening is follow-up.
		s := newPluginSimState()
		got, ok := s.simulateBusEvent("session.status", idleProps("childOrphan"))
		if !ok || got.Name != "PdxStop" {
			t.Fatalf("unseen child idle still emits PdxStop (documented limitation); ok=%v name=%q", ok, got.Name)
		}
		s2 := newPluginSimState()
		got2, ok2 := s2.simulateBusEvent("session.error", errorProps("childOrphan"))
		if !ok2 || got2.Name != "PdxStopFailure" {
			t.Fatalf("unseen child error still emits PdxStopFailure (documented limitation); ok=%v name=%q", ok2, got2.Name)
		}
	})

	t.Run("empty_sid_never_pollutes_map_and_parent_delete_not_swallowed", func(t *testing.T) {
		s := newPluginSimState()
		// Child created with parentID but no resolvable sid (no sessionID,
		// no info.id). The start is still suppressed, but '' must never be
		// keyed into the map — an empty key would then swallow the next
		// empty-sid parent delete.
		if _, ok := s.simulateBusEvent("session.created", map[string]any{
			"info": map[string]any{"parentID": "parent1"},
		}); ok {
			t.Fatal("child created with empty sid must not emit PdxSessionStart")
		}
		if _, exists := s.subagentSessions[""]; exists {
			t.Fatalf("empty sid must never be keyed into subagentSessions; map=%v", s.subagentSessions)
		}
		// A parent session.deleted with no sid and no info.parentID must
		// still emit PdxSessionEnd — never be swallowed as a child.
		got, ok := s.simulateBusEvent("session.deleted", map[string]any{})
		if !ok || got.Name != "PdxSessionEnd" {
			t.Fatalf("parent delete (empty sid, no parentID) must emit PdxSessionEnd; ok=%v name=%q", ok, got.Name)
		}
	})
}

// TestOpenCodePluginTemplate_RenderedTemplateContainsExpectedFilter
// guards the Decision 3 / Decision 4 filter line that turns a
// `session.status` Bus subscription into an idle-only Stop signal. If
// someone deletes the line, the template would emit Stop on every
// status transition (busy/retry included), spamming the daemon and
// flipping the state machine wrong.
func TestOpenCodePluginTemplate_RenderedTemplateContainsExpectedFilter(t *testing.T) {
	body := renderManagedPlugin("/fake/pdx")
	const filterLine = "event.properties.status?.type !== 'idle'"
	if !strings.Contains(body, filterLine) {
		t.Fatalf("rendered template missing Decision 3/4 idle filter %q", filterLine)
	}
}

// TestPluginTemplate_SessionCreated_EmitsCwd guards the tab-rebuild
// requirement (spec 2026-09-07 §3.1 / §4.2) that the parent-session start
// payload carries the working directory. Without it the rebuild record has
// no directory to recreate the tmux session in, and the resume has to fall
// back to `opencode -c`.
func TestPluginTemplate_SessionCreated_EmitsCwd(t *testing.T) {
	body := renderManagedPlugin("/usr/local/bin/pdx")
	if !strings.Contains(body, "cwd: pdxCwd()") {
		t.Fatalf("session.created emit does not include cwd:\n%s", body)
	}
	if !strings.Contains(body, "function pdxCwd()") {
		t.Fatalf("pdxCwd helper missing")
	}
}

// TestOpenCodePluginTemplate_ChildChatMessageOmitsSessionID keeps the Go
// mirror honest about the child-prompt gate the JS template grew in
// plan 2026-09-07 Task 4b.
//
// simulateChatMessage is a hand-written mirror of the JS `chat.message`
// handler, and the contract test drives it as if it were the plugin. Before
// this test the mirror still returned session_id for a known child session,
// so the contract suite would have stayed green with the JS gate deleted —
// a mirror that is knowingly wrong about a correctness-critical gate is
// worse than no mirror. The Bun runtime test remains the real authority.
//
// `cwd` is deliberately NOT modelled here: it never was, and that
// pre-existing approximation is not something this test starts fixing.
func TestOpenCodePluginTemplate_ChildChatMessageOmitsSessionID(t *testing.T) {
	t.Run("child_prompt_omits_session_id", func(t *testing.T) {
		s := newPluginSimState()
		s.simulateBusEvent("session.created", childCreatedProps("child1", "parent1"))

		got, ok := s.simulateStrongHook("chat.message",
			map[string]any{"sessionID": "child1", "messageID": "msg_c"},
			map[string]any{"message": map[string]any{"id": "msg_c", "agent": "build"}},
		)
		if !ok || got.Name != "PdxUserPromptSubmit" {
			t.Fatalf("child chat.message must still emit PdxUserPromptSubmit; ok=%v name=%q", ok, got.Name)
		}
		if _, exists := got.Payload["session_id"]; exists {
			t.Fatalf("child chat.message must omit session_id entirely; payload=%v", got.Payload)
		}
		// Everything else stays: suppressing the event, or blanking other
		// fields, would change the lights and is out of scope.
		if mid := strMapVal(got.Payload, "message_id"); mid != "msg_c" {
			t.Fatalf("message_id = %q, want %q", mid, "msg_c")
		}
		if agent := strMapVal(got.Payload, "agent"); agent != "build" {
			t.Fatalf("agent = %q, want %q", agent, "build")
		}
		if src := strMapVal(got.Payload, "source"); src != "chat.message" {
			t.Fatalf("source = %q, want %q", src, "chat.message")
		}
	})

	t.Run("parent_prompt_keeps_session_id", func(t *testing.T) {
		s := newPluginSimState()
		s.simulateBusEvent("session.created", childCreatedProps("child1", "parent1"))

		got, ok := s.simulateStrongHook("chat.message",
			map[string]any{"sessionID": "parent1", "messageID": "msg_p"},
			map[string]any{"message": map[string]any{"id": "msg_p", "agent": "build"}},
		)
		if !ok || got.Name != "PdxUserPromptSubmit" {
			t.Fatalf("parent chat.message must emit PdxUserPromptSubmit; ok=%v name=%q", ok, got.Name)
		}
		if sid := strMapVal(got.Payload, "session_id"); sid != "parent1" {
			t.Fatalf("session_id = %q, want %q", sid, "parent1")
		}
	})

	t.Run("unknown_session_keeps_session_id_reload_window", func(t *testing.T) {
		// Documented limit of the JS gate, mirrored: subagentSessions is
		// in-memory, so after a plugin reload an unseen child's prompt is
		// indistinguishable from a parent's and still carries session_id.
		s := newPluginSimState()
		got, ok := s.simulateStrongHook("chat.message",
			map[string]any{"sessionID": "childOrphan", "messageID": "msg_o"},
			map[string]any{"message": map[string]any{"id": "msg_o"}},
		)
		if !ok {
			t.Fatal("unseen session chat.message must emit")
		}
		if sid := strMapVal(got.Payload, "session_id"); sid != "childOrphan" {
			t.Fatalf("session_id = %q, want %q (documented reload-window limit)", sid, "childOrphan")
		}
	})

	t.Run("stale_error_suppression_is_still_cleared_for_a_child", func(t *testing.T) {
		// The gate drops a field from the payload; it must not skip the
		// handler's own bookkeeping.
		s := newPluginSimState()
		s.simulateBusEvent("session.created", childCreatedProps("child1", "parent1"))
		s.suppressIdleForSession["child1"] = true
		s.simulateChatMessage(
			map[string]any{"sessionID": "child1", "messageID": "msg_c"},
			map[string]any{"message": map[string]any{"id": "msg_c"}},
		)
		if s.suppressIdleForSession["child1"] {
			t.Fatal("child chat.message must still clear stale suppressIdleForSession")
		}
	})
}
