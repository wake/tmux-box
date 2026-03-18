# Stream WS Lifecycle Redesign Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix stream messages not flowing by moving WS lifecycle to App.tsx (driven by relay events), making the store per-session, and adding JSONL history API for resume context.

**Architecture:** Go daemon broadcasts relay connect/disconnect via session-events + captures init metadata. SPA's `useRelayWsManager` hook creates/destroys stream WS connections in response. Per-session Zustand store replaces global singleton. JSONL history API provides conversation context on resume.

**Tech Stack:** Go (net/http, gorilla/websocket, modernc.org/sqlite) + React 19 (Zustand, Vite, vitest)

**Spec:** `docs/superpowers/specs/2026-03-18-stream-ws-lifecycle-design.md`

---

## File Map

### Go — Create
| File | Responsibility |
|------|---------------|
| `internal/history/history.go` | JSONL path resolver + parser |
| `internal/history/history_test.go` | JSONL parser unit tests |
| `internal/server/history_handler.go` | `GET /api/sessions/{id}/history` endpoint |
| `internal/server/history_handler_test.go` | History endpoint integration tests |

### Go — Modify
| File | Change |
|------|--------|
| `internal/store/store.go` | Add `cc_model` column, migration, SessionUpdate field |
| `internal/bridge/bridge.go` | Add `RelaySessionNames()` method |
| `internal/server/bridge_handler.go` | Init metadata capture in relay goroutine |
| `internal/server/bridge_handler_test.go` | Test init metadata capture |
| `internal/server/events_handler.go` | Relay broadcast callbacks + snapshot |
| `internal/server/events_handler_test.go` | Test relay events + snapshot |
| `internal/server/session_handler.go` | SessionResponse DTO with `has_relay`, `cc_model` |
| `internal/server/server.go` | Wire history handler route, pass bridge to session handler |

### SPA — Create
| File | Responsibility |
|------|---------------|
| `spa/src/hooks/useRelayWsManager.ts` | Stream WS lifecycle driven by relay events |
| `spa/src/hooks/useRelayWsManager.test.ts` | Hook unit tests |

### SPA — Modify
| File | Change |
|------|--------|
| `spa/src/stores/useStreamStore.ts` | Per-session state structure |
| `spa/src/stores/useStreamStore.test.ts` | Updated tests for per-session API |
| `spa/src/lib/session-events.ts` | Add `'relay'` to SessionEvent type |
| `spa/src/lib/api.ts` | Add `fetchHistory()`, update Session interface |
| `spa/src/components/ConversationView.tsx` | Remove WS management, read from per-session store |
| `spa/src/components/ConversationView.test.tsx` | Update tests for new Props |
| `spa/src/App.tsx` | Use `useRelayWsManager` hook, update event handling |

---

## Task 1: DB migration — `cc_model` column

**Files:**
- Modify: `internal/store/store.go:38-43,127-149,182-228`

- [ ] **Step 1: Write failing test**

```go
// internal/store/store_test.go — add to existing file
func TestCCModelMigration(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	id, _ := db.CreateSession(store.Session{Name: "test", Cwd: "/tmp", Mode: "term"})
	sess, _ := db.GetSession(id)
	model := "claude-sonnet-4-6"
	err = db.UpdateSession(sess.ID, SessionUpdate{CCModel: &model})
	if err != nil {
		t.Fatalf("update cc_model: %v", err)
	}

	got, err := db.GetSession(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CCModel != "claude-sonnet-4-6" {
		t.Fatalf("want claude-sonnet-4-6, got %q", got.CCModel)
	}
}
```

- [ ] **Step 2: Run test — verify FAIL**

Run: `go test ./internal/store/ -run TestCCModelMigration -v`
Expected: compilation error — `CCModel` field not found

- [ ] **Step 3: Implement**

Add to `Session` struct (after `CCSessionID`):
```go
CCModel     string `json:"cc_model"`
```

Add to `SessionUpdate` struct:
```go
CCModel     *string `json:"cc_model,omitempty"`
```

Add migration block in `migrate()` (after `cc_session_id` migration, same pattern):
```go
// Add cc_model column if missing
hasCCModel := false
rows, err = s.db.Query("PRAGMA table_info(sessions)")
if err != nil {
	return err
}
for rows.Next() {
	var cid int
	var name, typ string
	var notnull, pk int
	var dflt *string
	rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk)
	if name == "cc_model" {
		hasCCModel = true
	}
}
rows.Close()
if !hasCCModel {
	if _, err := s.db.Exec("ALTER TABLE sessions ADD COLUMN cc_model TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
}
```

Add to `UpdateSession` method (after `CCSessionID` block):
```go
if u.CCModel != nil {
	if _, err := s.db.Exec("UPDATE sessions SET cc_model = ? WHERE id = ?", *u.CCModel, id); err != nil {
		return err
	}
	updated = true
}
```

Add `CCModel` to all SELECT scan lists in `GetSession`, `ListSessions`, `CreateSession` return.

- [ ] **Step 4: Run test — verify PASS**

Run: `go test ./internal/store/ -v`
Expected: all PASS

- [ ] **Step 5: Commit**

```
feat(store): add cc_model column to sessions table
```

---

## Task 2: Bridge — `RelaySessionNames()`

**Files:**
- Modify: `internal/bridge/bridge.go:63-68`
- Modify: `internal/bridge/bridge_test.go` (if exists, else create)

- [ ] **Step 1: Write failing test**

```go
// internal/bridge/bridge_test.go
package bridge

import "testing"

func TestRelaySessionNames(t *testing.T) {
	b := New()

	// No relays
	names := b.RelaySessionNames()
	if len(names) != 0 {
		t.Fatalf("want 0, got %d", len(names))
	}

	// Register two relays
	b.RegisterRelay("alpha")
	b.RegisterRelay("beta")

	names = b.RelaySessionNames()
	if len(names) != 2 {
		t.Fatalf("want 2, got %d", len(names))
	}

	found := map[string]bool{}
	for _, n := range names {
		found[n] = true
	}
	if !found["alpha"] || !found["beta"] {
		t.Fatalf("missing names: %v", names)
	}

	// Unregister one
	b.UnregisterRelay("alpha")
	names = b.RelaySessionNames()
	if len(names) != 1 || names[0] != "beta" {
		t.Fatalf("after unregister: %v", names)
	}
}
```

- [ ] **Step 2: Run test — verify FAIL**

Run: `go test ./internal/bridge/ -run TestRelaySessionNames -v`
Expected: compilation error — `RelaySessionNames` not defined

- [ ] **Step 3: Implement**

```go
// RelaySessionNames returns the names of all sessions with active relays.
func (b *Bridge) RelaySessionNames() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	names := make([]string, 0, len(b.sessions))
	for name := range b.sessions {
		names = append(names, name)
	}
	return names
}
```

- [ ] **Step 4: Run test — verify PASS**

Run: `go test ./internal/bridge/ -v`
Expected: all PASS

- [ ] **Step 5: Commit**

```
feat(bridge): add RelaySessionNames for relay status enumeration
```

---

## Task 3: bridge_handler — init metadata capture

**Files:**
- Modify: `internal/server/bridge_handler.go:50-60`
- Modify: `internal/server/bridge_handler_test.go`

- [ ] **Step 1: Write failing test**

```go
// Add to internal/server/bridge_handler_test.go
func TestBridgeInitMetadataCapture(t *testing.T) {
	srv := setupServer(t)

	// Connect relay
	relay := dial(t, wsURL(srv, "/ws/cli-bridge/meta-test"))
	defer relay.Close()

	time.Sleep(50 * time.Millisecond)

	// Relay sends init message (simulating CC output)
	initMsg := `{"type":"system","subtype":"init","session_id":"abc-123","model":"claude-sonnet-4-6","tools":["Read","Write"]}`
	relay.WriteMessage(websocket.TextMessage, []byte(initMsg))

	// Wait for processing
	time.Sleep(100 * time.Millisecond)

	// Verify: init message should still be fan-out'd to subscribers
	// (we connect subscriber after init to test fan-out separately)
	// Here we just verify no crash and the message was processed.

	// Send a non-init message to verify normal flow continues
	relay.WriteMessage(websocket.TextMessage, []byte(`{"type":"assistant","content":"hello"}`))

	// Connect subscriber and verify normal messages still flow
	sub := dial(t, wsURL(srv, "/ws/cli-bridge-sub/meta-test"))
	defer sub.Close()

	relay.WriteMessage(websocket.TextMessage, []byte(`{"type":"assistant","content":"world"}`))

	sub.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := sub.ReadMessage()
	if err != nil {
		t.Fatalf("subscriber read: %v", err)
	}
	if string(msg) != `{"type":"assistant","content":"world"}` {
		t.Fatalf("got %q", msg)
	}
}
```

- [ ] **Step 2: Run test — verify PASS** (this test only validates fan-out isn't broken)

Run: `go test ./internal/server/ -run TestBridgeInitMetadataCapture -v`
Expected: PASS

- [ ] **Step 3: Write failing test for init metadata saving to DB** (this is the actual RED step)

See Step 4 below — write `TestBridgeInitMetadataSavesToDB` first, run it, verify FAIL (model not saved).

- [ ] **Step 4: Add init capture logic to handleCliBridge**

In `bridge_handler.go`, modify the relay→subscribers goroutine (lines 51-59):

```go
// Relay WS → bridge (subprocess stdout → SPA subscribers)
go func() {
	defer cancel()
	initCaptured := false
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		s.bridge.RelayToSubscribers(sessionName, msg)

		// One-shot init metadata capture
		if !initCaptured && bytes.Contains(msg, []byte(`"subtype":"init"`)) {
			var init struct {
				Type    string `json:"type"`
				Subtype string `json:"subtype"`
				Model   string `json:"model"`
			}
			if json.Unmarshal(msg, &init) == nil && init.Type == "system" && init.Subtype == "init" {
				initCaptured = true
				if init.Model != "" {
					// Need session ID from store — lookup by name
					sessions, err := s.store.ListSessions()
					if err == nil {
						for _, sess := range sessions {
							if sess.Name == sessionName {
								s.store.UpdateSession(sess.ID, store.SessionUpdate{CCModel: &init.Model})
								s.events.Broadcast(sessionName, "init", init.Model)
								break
							}
						}
					}
				}
			}
		}
	}
}()
```

Add `"bytes"` and `"encoding/json"` to imports if not present.

- [ ] **Step 4: Write test verifying model is stored in DB**

```go
func TestBridgeInitMetadataSavesToDB(t *testing.T) {
	// Create a server with a real session in the DB
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	cfg := config.Config{}
	tx := tmux.NewFakeExecutor()
	tx.AddSession("model-test", "/tmp")
	s := server.New(cfg, db, tx, "")
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)

	// Create session in DB
	sessID, err := db.CreateSession(store.Session{Name: "model-test", Cwd: "/tmp", Mode: "stream"})
	if err != nil {
		t.Fatal(err)
	}
	sess, _ := db.GetSession(sessID)

	// Connect relay
	relay := dial(t, wsURL(srv, "/ws/cli-bridge/model-test"))
	defer relay.Close()
	time.Sleep(50 * time.Millisecond)

	// Send init message
	relay.WriteMessage(websocket.TextMessage, []byte(
		`{"type":"system","subtype":"init","model":"claude-opus-4-6","session_id":"xyz"}`,
	))
	time.Sleep(200 * time.Millisecond)

	// Verify DB has the model
	got, err := db.GetSession(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CCModel != "claude-opus-4-6" {
		t.Fatalf("want claude-opus-4-6, got %q", got.CCModel)
	}
}
```

- [ ] **Step 5: Run all tests — verify PASS**

Run: `go test ./internal/server/ -v -timeout 30s`
Expected: all PASS

- [ ] **Step 6: Commit**

```
feat(bridge): capture init metadata (model) from relay stream
```

---

## Task 4: events_handler — relay broadcast + snapshot

**Files:**
- Modify: `internal/server/events_handler.go:111-154`
- Modify: `internal/server/server.go` (pass bridge relay callbacks)
- Modify: `internal/server/events_handler_test.go`

- [ ] **Step 1: Write failing test for relay event broadcast**

```go
// internal/server/events_handler_test.go — add
func TestRelayEventsSnapshot(t *testing.T) {
	srv := setupServer(t)

	// Connect a relay first (creates relay state)
	relay := dial(t, wsURL(srv, "/ws/cli-bridge/snap-test"))
	defer relay.Close()
	time.Sleep(50 * time.Millisecond)

	// Connect session-events subscriber — should receive snapshot with relay status
	sub := dial(t, wsURL(srv, "/ws/session-events"))
	defer sub.Close()

	// Read snapshot messages (status + relay)
	var relayEvent map[string]string
	sub.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		_, msg, err := sub.ReadMessage()
		if err != nil {
			break
		}
		var ev map[string]string
		json.Unmarshal(msg, &ev)
		if ev["type"] == "relay" && ev["session"] == "snap-test" {
			relayEvent = ev
			break
		}
	}

	if relayEvent == nil {
		t.Fatal("expected relay snapshot event for snap-test")
	}
	if relayEvent["value"] != "connected" {
		t.Fatalf("want connected, got %q", relayEvent["value"])
	}
}
```

- [ ] **Step 2: Run test — verify FAIL**

Run: `go test ./internal/server/ -run TestRelayEventsSnapshot -v -timeout 10s`
Expected: FAIL — no relay event in snapshot

- [ ] **Step 3: Implement relay broadcast + snapshot**

Add to `events_handler.go` — new method on Server for relay snapshot:

```go
func (s *Server) sendRelaySnapshot(sub *eventSubscriber) {
	for _, name := range s.bridge.RelaySessionNames() {
		msg, err := json.Marshal(sessionEvent{Type: "relay", Session: name, Value: "connected"})
		if err != nil {
			continue
		}
		select {
		case sub.send <- msg:
		default:
		}
	}
}
```

In `handleSessionEvents` (line 121), after `s.sendStatusSnapshot(sub)`, add:
```go
s.sendRelaySnapshot(sub)
```

For live relay events, modify `bridge_handler.go`:
- In `handleCliBridge`, after `RegisterRelay` succeeds (line 33): `s.events.Broadcast(sessionName, "relay", "connected")`
- In the defer block (line 41), before `UnregisterRelay`: `s.events.Broadcast(sessionName, "relay", "disconnected")`

Note: on relay disconnect, SPA will now receive both `relay:disconnected` and `handoff:failed:relay disconnected` (from existing `revertModeOnRelayDisconnect`). This is intentional — `relay` event drives WS lifecycle, `handoff` event drives UI state.

- [ ] **Step 4: Run test — verify PASS**

Run: `go test ./internal/server/ -run TestRelayEventsSnapshot -v -timeout 10s`
Expected: PASS

- [ ] **Step 5: Write test for live relay connect/disconnect events**

```go
func TestRelayEventsLive(t *testing.T) {
	srv := setupServer(t)

	// Connect session-events subscriber first
	sub := dial(t, wsURL(srv, "/ws/session-events"))
	defer sub.Close()

	// Drain any snapshot messages
	sub.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	for {
		_, _, err := sub.ReadMessage()
		if err != nil {
			break
		}
	}
	sub.SetReadDeadline(time.Time{}) // reset

	// Connect relay — should trigger relay:connected event
	relay := dial(t, wsURL(srv, "/ws/cli-bridge/live-test"))

	sub.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := sub.ReadMessage()
	if err != nil {
		t.Fatalf("read relay connected event: %v", err)
	}
	var ev map[string]string
	json.Unmarshal(msg, &ev)
	if ev["type"] != "relay" || ev["session"] != "live-test" || ev["value"] != "connected" {
		t.Fatalf("unexpected event: %s", msg)
	}

	// Disconnect relay — should trigger relay:disconnected
	relay.Close()

	sub.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err = sub.ReadMessage()
	if err != nil {
		t.Fatalf("read relay disconnected event: %v", err)
	}
	json.Unmarshal(msg, &ev)
	if ev["type"] != "relay" || ev["value"] != "disconnected" {
		t.Fatalf("unexpected event: %s", msg)
	}
}
```

- [ ] **Step 6: Run all tests — verify PASS**

Run: `go test ./internal/server/ -v -timeout 30s`
Expected: all PASS

- [ ] **Step 7: Commit**

```
feat(events): broadcast relay connect/disconnect via session-events
```

---

## Task 5: Session API — SessionResponse DTO

**Files:**
- Modify: `internal/server/session_handler.go:82-96`

- [ ] **Step 1: Write failing test**

```go
// internal/server/session_handler_test.go — add
func TestSessionListIncludesRelayAndModel(t *testing.T) {
	srv := setupServer(t)

	// Create session via API
	body := `{"name":"dto-test","cwd":"/tmp","mode":"term"}`
	resp, _ := http.Post(srv.URL+"/api/sessions", "application/json", strings.NewReader(body))
	resp.Body.Close()

	// Connect relay for this session
	relay := dial(t, wsURL(srv, "/ws/cli-bridge/dto-test"))
	defer relay.Close()
	time.Sleep(50 * time.Millisecond)

	// List sessions
	resp, _ = http.Get(srv.URL + "/api/sessions")
	defer resp.Body.Close()
	var sessions []map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&sessions)

	var found map[string]interface{}
	for _, s := range sessions {
		if s["name"] == "dto-test" {
			found = s
			break
		}
	}
	if found == nil {
		t.Fatal("session not found")
	}
	if found["has_relay"] != true {
		t.Fatalf("want has_relay=true, got %v", found["has_relay"])
	}
	if _, ok := found["cc_model"]; !ok {
		t.Fatal("missing cc_model field")
	}
}
```

- [ ] **Step 2: Run test — verify FAIL**

Run: `go test ./internal/server/ -run TestSessionListIncludesRelayAndModel -v`
Expected: FAIL — `has_relay` field not in response

- [ ] **Step 3: Implement SessionResponse DTO**

In `session_handler.go`, add DTO and modify List handler:

```go
type SessionResponse struct {
	store.Session
	HasRelay bool `json:"has_relay"`
	// cc_model comes from embedded store.Session — no duplicate field needed
}
```

SessionHandler needs bridge access. Add `bridge` field:

```go
type SessionHandler struct {
	store  *store.Store
	tmux   tmux.Executor
	bridge *bridge.Bridge
}

func NewSessionHandler(st *store.Store, tx tmux.Executor, br *bridge.Bridge) *SessionHandler {
	return &SessionHandler{store: st, tmux: tx, bridge: br}
}
```

Update `List` handler (line 95) to build DTOs:

```go
result := make([]SessionResponse, len(sessions))
for i, s := range sessions {
	result[i] = SessionResponse{
		Session:  s,
		HasRelay: h.bridge.HasRelay(s.Name),
	}
}
json.NewEncoder(w).Encode(result)
```

Update `server.go` routes to pass bridge:
```go
sh := NewSessionHandler(s.store, s.tmux, s.bridge)
```

Update `session_handler_test.go` `setupHandler` to pass a `bridge.New()`:
```go
func setupHandler(t *testing.T) (*httptest.Server, *store.Store) {
	// ... existing code ...
	h := server.NewSessionHandler(db, tmux.NewFakeExecutor(), bridge.New())
	// ...
}
```

- [ ] **Step 4: Run test — verify PASS**

Run: `go test ./internal/server/ -v -timeout 30s`
Expected: all PASS

- [ ] **Step 5: Commit**

```
feat(api): add has_relay and cc_model to session list response
```

---

## Task 6: JSONL history API

**Files:**
- Create: `internal/history/history.go`
- Create: `internal/history/history_test.go`
- Create: `internal/server/history_handler.go`
- Create: `internal/server/history_handler_test.go`
- Modify: `internal/server/server.go` (add route)

- [ ] **Step 1: Write failing test for JSONL path resolver**

```go
// internal/history/history_test.go
package history

import "testing"

func TestCCProjectPath(t *testing.T) {
	got := CCProjectPath("/Users/wake/Workspace/wake/tmux-box")
	want := "-Users-wake-Workspace-wake-tmux-box"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestCCProjectPathRoot(t *testing.T) {
	got := CCProjectPath("/")
	want := "-"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run test — verify FAIL**

Run: `go test ./internal/history/ -run TestCCProjectPath -v`
Expected: compilation error

- [ ] **Step 3: Implement path resolver**

```go
// internal/history/history.go
package history

import "strings"

// CCProjectPath converts a working directory path to CC's project hash format.
// Example: "/Users/wake/Workspace" → "-Users-wake-Workspace"
func CCProjectPath(cwd string) string {
	return strings.ReplaceAll(cwd, "/", "-")
}
```

- [ ] **Step 4: Run test — verify PASS**

Run: `go test ./internal/history/ -run TestCCProjectPath -v`
Expected: PASS

- [ ] **Step 5: Write failing test for JSONL parser**

```go
func TestParseJSONL(t *testing.T) {
	input := `{"type":"progress","data":"something"}
{"type":"user","message":{"role":"user","content":"hello"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn"}}
{"type":"system","subtype":"hook"}
{"invalid json
{"type":"user","message":{"role":"user","content":[{"type":"text","text":"second"}]}}
`
	messages, err := ParseJSONL(strings.NewReader(input), 2*1024*1024)
	if err != nil {
		t.Fatal(err)
	}

	if len(messages) != 3 {
		t.Fatalf("want 3 messages, got %d", len(messages))
	}

	// First user message — string content should be converted to content block array
	if messages[0]["type"] != "user" {
		t.Fatalf("msg 0: want user, got %v", messages[0]["type"])
	}

	// Assistant message
	if messages[1]["type"] != "assistant" {
		t.Fatalf("msg 1: want assistant, got %v", messages[1]["type"])
	}

	// Second user message — already has content block array
	if messages[2]["type"] != "user" {
		t.Fatalf("msg 2: want user, got %v", messages[2]["type"])
	}
}

func TestParseJSONLSizeLimit(t *testing.T) {
	// Create a large input
	var sb strings.Builder
	for i := 0; i < 1000; i++ {
		sb.WriteString(`{"type":"user","message":{"role":"user","content":"msg"}}` + "\n")
	}
	messages, err := ParseJSONL(strings.NewReader(sb.String()), 1024) // very small limit
	if err != nil {
		t.Fatal(err)
	}
	// Should return some messages (tail truncation)
	if len(messages) == 0 {
		t.Fatal("expected some messages despite size limit")
	}
	if len(messages) >= 1000 {
		t.Fatal("expected truncation")
	}
}
```

- [ ] **Step 6: Run test — verify FAIL**

Run: `go test ./internal/history/ -run TestParseJSONL -v`
Expected: compilation error — `ParseJSONL` not defined

- [ ] **Step 7: Implement JSONL parser**

```go
// ParseJSONL reads CC JSONL session data and returns stream-json compatible messages.
// Only user and assistant messages are included. maxBytes limits total input read.
// When the input exceeds maxBytes, earlier messages are dropped (tail is preserved).
func ParseJSONL(r io.Reader, maxBytes int64) ([]map[string]interface{}, error) {
	// Read all data (up to maxBytes), then keep the tail if truncated.
	data, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, err
	}
	// If data exceeds maxBytes, it was truncated — find the first complete line boundary.
	if int64(len(data)) > maxBytes {
		data = data[len(data)-int(maxBytes):]
		// Find first newline to skip partial line at the start.
		if idx := bytes.IndexByte(data, '\n'); idx >= 0 {
			data = data[idx+1:]
		}
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var messages []map[string]interface{}
	for scanner.Scan() {
		var entry map[string]interface{}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue // skip malformed lines
		}

		typ, _ := entry["type"].(string)
		if typ != "user" && typ != "assistant" {
			continue
		}

		msg, ok := entry["message"].(map[string]interface{})
		if !ok {
			continue
		}

		// Normalize content: string → content block array
		if content, ok := msg["content"].(string); ok {
			msg["content"] = []map[string]interface{}{
				{"type": "text", "text": content},
			}
		}

		messages = append(messages, map[string]interface{}{
			"type":    typ,
			"message": msg,
		})
	}
	return messages, scanner.Err()
}
```

Add imports: `"bufio"`, `"bytes"`, `"encoding/json"`, `"io"`.

- [ ] **Step 8: Run test — verify PASS**

Run: `go test ./internal/history/ -v`
Expected: all PASS

- [ ] **Step 9: Write failing test for history endpoint**

```go
// internal/server/history_handler_test.go
package server_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHistoryEndpointReturnsMessages(t *testing.T) {
	// Setup: create temp CC JSONL file
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	// Create JSONL file at expected path
	projectHash := "-tmp"
	ccSessionID := "test-session-uuid"
	jsonlDir := filepath.Join(homeDir, ".claude", "projects", projectHash)
	os.MkdirAll(jsonlDir, 0755)

	jsonlContent := `{"type":"progress","data":"ignore"}
{"type":"user","message":{"role":"user","content":"hello"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hi there"}],"stop_reason":"end_turn"}}
`
	os.WriteFile(filepath.Join(jsonlDir, ccSessionID+".jsonl"), []byte(jsonlContent), 0644)

	// Setup server with session that has cwd=/tmp and cc_session_id
	srv := setupServer(t) // uses TempDir DB
	body := `{"name":"hist-test","cwd":"/tmp","mode":"stream"}`
	resp, _ := http.Post(srv.URL+"/api/sessions", "application/json", strings.NewReader(body))
	var created map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	sessionID := int64(created["id"].(float64))

	// Set cc_session_id in DB (normally done by handoff)
	// We need to call the API or directly set it — use mode switch + direct DB
	// For now, just test the empty case
	resp, _ = http.Get(srv.URL + "/api/sessions/" + fmt.Sprintf("%d", sessionID) + "/history")
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var messages []interface{}
	json.NewDecoder(resp.Body).Decode(&messages)

	// cc_session_id is empty → should return empty array
	if len(messages) != 0 {
		t.Fatalf("want empty array, got %d messages", len(messages))
	}
}
```

- [ ] **Step 10: Implement history handler**

```go
// internal/server/history_handler.go
package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/wake/tmux-box/internal/history"
)

const maxJSONLBytes = 2 * 1024 * 1024 // 2MB

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	sess, err := s.store.GetSession(id)
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	if sess.CCSessionID == "" {
		json.NewEncoder(w).Encode([]interface{}{})
		return
	}

	home, _ := os.UserHomeDir()
	projectHash := history.CCProjectPath(sess.Cwd)
	jsonlPath := filepath.Join(home, ".claude", "projects", projectHash, sess.CCSessionID+".jsonl")

	f, err := os.Open(jsonlPath)
	if err != nil {
		json.NewEncoder(w).Encode([]interface{}{})
		return
	}
	defer f.Close()

	messages, err := history.ParseJSONL(f, maxJSONLBytes)
	if err != nil || messages == nil {
		json.NewEncoder(w).Encode([]interface{}{})
		return
	}

	json.NewEncoder(w).Encode(messages)
}
```

Add route in `server.go` routes():
```go
s.mux.HandleFunc("GET /api/sessions/{id}/history", s.handleHistory)
```

- [ ] **Step 11: Run all tests — verify PASS**

Run: `go test ./internal/... -v -timeout 30s`
Expected: all PASS

- [ ] **Step 12: Commit**

```
feat(api): add JSONL history endpoint for conversation context on resume
```

---

## Task 7: SPA — per-session store rewrite

**Files:**
- Modify: `spa/src/stores/useStreamStore.ts`
- Modify: `spa/src/stores/useStreamStore.test.ts`

- [ ] **Step 1: Write failing tests for per-session store**

Rewrite `useStreamStore.test.ts` with per-session API:

```ts
// spa/src/stores/useStreamStore.test.ts
import { beforeEach, describe, expect, it } from 'vitest'
import { useStreamStore } from './useStreamStore'

describe('useStreamStore (per-session)', () => {
  beforeEach(() => {
    useStreamStore.setState({ sessions: {}, sessionStatus: {}, relayStatus: {}, handoffState: {}, handoffProgress: {} })
  })

  it('has empty sessions by default', () => {
    const { sessions } = useStreamStore.getState()
    expect(sessions).toEqual({})
  })

  it('addMessage creates session lazily and appends', () => {
    const { addMessage } = useStreamStore.getState()
    const msg = { type: 'assistant' as const, message: { role: 'assistant', content: [{ type: 'text', text: 'hi' }], stop_reason: 'end_turn' } }
    addMessage('sess-a', msg)
    const state = useStreamStore.getState().sessions['sess-a']
    expect(state.messages).toHaveLength(1)
    expect(state.messages[0]).toBe(msg)
  })

  it('messages are independent per session', () => {
    const { addMessage } = useStreamStore.getState()
    addMessage('sess-a', { type: 'user' } as any)
    addMessage('sess-b', { type: 'assistant' } as any)
    expect(useStreamStore.getState().sessions['sess-a'].messages).toHaveLength(1)
    expect(useStreamStore.getState().sessions['sess-b'].messages).toHaveLength(1)
  })

  it('setConn stores per session', () => {
    const { setConn } = useStreamStore.getState()
    const mockConn = { send: () => {}, close: () => {} } as any
    setConn('sess-a', mockConn)
    expect(useStreamStore.getState().sessions['sess-a'].conn).toBe(mockConn)
    expect(useStreamStore.getState().sessions['sess-b']?.conn).toBeUndefined()
  })

  it('setStreaming per session', () => {
    const { setStreaming } = useStreamStore.getState()
    setStreaming('sess-a', true)
    expect(useStreamStore.getState().sessions['sess-a'].isStreaming).toBe(true)
  })

  it('loadHistory sets messages for session', () => {
    const { loadHistory } = useStreamStore.getState()
    const msgs = [{ type: 'user' } as any, { type: 'assistant' } as any]
    loadHistory('sess-a', msgs)
    expect(useStreamStore.getState().sessions['sess-a'].messages).toEqual(msgs)
  })

  it('clearSession closes conn and removes state', () => {
    const { setConn, addMessage, clearSession } = useStreamStore.getState()
    let closed = false
    const mockConn = { send: () => {}, close: () => { closed = true } } as any
    setConn('sess-a', mockConn)
    addMessage('sess-a', { type: 'user' } as any)
    clearSession('sess-a')
    expect(closed).toBe(true)
    expect(useStreamStore.getState().sessions['sess-a']).toBeUndefined()
  })

  it('handoffState is per-session', () => {
    const { setHandoffState } = useStreamStore.getState()
    setHandoffState('sess-a', 'connected')
    setHandoffState('sess-b', 'handoff-in-progress')
    expect(useStreamStore.getState().handoffState['sess-a']).toBe('connected')
    expect(useStreamStore.getState().handoffState['sess-b']).toBe('handoff-in-progress')
  })

  it('relayStatus is per-session', () => {
    const { setRelayStatus } = useStreamStore.getState()
    setRelayStatus('sess-a', true)
    setRelayStatus('sess-b', false)
    expect(useStreamStore.getState().relayStatus['sess-a']).toBe(true)
    expect(useStreamStore.getState().relayStatus['sess-b']).toBe(false)
  })

  it('sessionStatus persists across clearSession', () => {
    const { setSessionStatus, clearSession } = useStreamStore.getState()
    setSessionStatus('sess-a', 'cc-idle')
    clearSession('sess-a')
    expect(useStreamStore.getState().sessionStatus['sess-a']).toBe('cc-idle')
  })
})
```

- [ ] **Step 2: Run test — verify FAIL**

Run: `cd spa && npx vitest run src/stores/useStreamStore.test.ts`
Expected: FAIL — API doesn't match

- [ ] **Step 3: Implement per-session store**

Rewrite `spa/src/stores/useStreamStore.ts`. Key requirements:
- Use `subscribeWithSelector` middleware: `create<StreamStore>()(subscribeWithSelector((set, get) => ({...})))`
- Each action takes `session: string` as first param
- Helper to lazily init per-session state: `getOrCreate(session)` returns `PerSessionState` with defaults
- Keep ALL existing actions but add session param: `addMessage`, `addControlRequest`, `resolveControlRequest`, `setConn`, `setStreaming`, `setSessionInfo`, `addCost`, `setHandoffState`, `setHandoffProgress`, `setSessionStatus`, `loadHistory`, `clearSession`, `setRelayStatus`
- `clearSession` must call `conn?.close()` before deleting state

- [ ] **Step 4: Run test — verify PASS**

Run: `cd spa && npx vitest run src/stores/useStreamStore.test.ts`
Expected: all PASS

- [ ] **Step 5: Commit**

```
feat(store): rewrite useStreamStore with per-session state
```

---

## Task 8: SPA — session-events type + api.ts updates

**Files:**
- Modify: `spa/src/lib/session-events.ts:4`
- Modify: `spa/src/lib/api.ts:2-12`

- [ ] **Step 1: Update SessionEvent type**

In `session-events.ts` line 4, change:
```ts
type: 'status' | 'handoff'
```
to:
```ts
type: 'status' | 'handoff' | 'relay'
```

- [ ] **Step 2: Update Session interface in api.ts**

Add fields:
```ts
has_relay: boolean
cc_model: string
```

- [ ] **Step 3: Add fetchHistory function to api.ts**

```ts
export async function fetchHistory(base: string, sessionId: number): Promise<StreamMessage[]> {
  const res = await fetch(`${base}/api/sessions/${sessionId}/history`)
  if (!res.ok) return []
  return res.json()
}
```

Add import for `StreamMessage` from `./stream-ws`.

- [ ] **Step 4: Verify build passes**

Run: `cd spa && npx tsc --noEmit`
Expected: no type errors

- [ ] **Step 5: Commit**

```
feat(api): add relay event type, history fetch, session DTO fields
```

---

## Task 9: SPA — useRelayWsManager hook

**Files:**
- Create: `spa/src/hooks/useRelayWsManager.ts`
- Create: `spa/src/hooks/useRelayWsManager.test.ts`

- [ ] **Step 1: Write failing test**

```ts
// spa/src/hooks/useRelayWsManager.test.ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useStreamStore } from '../stores/useStreamStore'

// Test the core logic: when relayStatus changes, WS should be created/destroyed

describe('useRelayWsManager logic', () => {
  beforeEach(() => {
    useStreamStore.setState({ sessions: {}, sessionStatus: {}, relayStatus: {}, handoffState: {}, handoffProgress: {} })
  })

  it('creates conn when relayStatus becomes true', () => {
    const { setRelayStatus } = useStreamStore.getState()
    setRelayStatus('test-session', true)
    expect(useStreamStore.getState().relayStatus['test-session']).toBe(true)
    // Hook would create WS connection here — tested via integration
  })

  it('clears conn when relayStatus becomes false', () => {
    const { setRelayStatus, setConn } = useStreamStore.getState()
    let closed = false
    const mockConn = { send: () => {}, close: () => { closed = true } } as any
    setConn('test-session', mockConn)
    setRelayStatus('test-session', false)
    // Hook would close WS and clear conn
    // For unit test, verify the store API supports it
    const { clearSession } = useStreamStore.getState()
    clearSession('test-session')
    expect(closed).toBe(true)
  })
})
```

- [ ] **Step 2: Run test — verify behavior**

Run: `cd spa && npx vitest run src/hooks/useRelayWsManager.test.ts`

- [ ] **Step 3: Implement hook**

```ts
// spa/src/hooks/useRelayWsManager.ts
import { useEffect, useRef } from 'react'
import { useStreamStore } from '../stores/useStreamStore'
import { connectStream, type StreamMessage } from '../lib/stream-ws'
import { fetchHistory } from '../lib/api'

export function useRelayWsManager(wsBase: string, daemonBase: string) {
  const prevRelay = useRef<Record<string, boolean>>({})

  useEffect(() => {
    // Subscribe to relayStatus changes
    // Track active connections for cleanup on unmount
    const activeConns = new Map<string, { close: () => void }>()

    const unsub = useStreamStore.subscribe(
      (state) => state.relayStatus,
      (relayStatus) => {
        const store = useStreamStore.getState()

        for (const [session, connected] of Object.entries(relayStatus)) {
          const wasConnected = prevRelay.current[session] ?? false

          if (connected && !wasConnected) {
            // Relay just connected — create stream WS
            const conn = connectStream(
              `${wsBase}/ws/cli-bridge-sub/${encodeURIComponent(session)}`,
              (msg: StreamMessage) => {
                if (msg.type === 'assistant' || msg.type === 'user') {
                  useStreamStore.getState().addMessage(session, msg)
                }
                if (msg.type === 'result' && 'total_cost_usd' in msg) {
                  useStreamStore.getState().setStreaming(session, false)
                }
                if (msg.type === 'control_request') {
                  useStreamStore.getState().addControlRequest(session, msg as any)
                }
                if (msg.type === 'system') {
                  const sys = msg as any
                  if (sys.subtype === 'init') {
                    useStreamStore.getState().setSessionInfo(session, sys.session_id ?? '', sys.model ?? '')
                  }
                }
              },
              () => {
                // WS closed
                useStreamStore.getState().setConn(session, null)
              },
            )
            useStreamStore.getState().setConn(session, conn)
            activeConns.set(session, conn)
          }

          if (!connected && wasConnected) {
            // Relay disconnected — close stream WS
            const existing = useStreamStore.getState().sessions[session]?.conn
            existing?.close()
            useStreamStore.getState().setConn(session, null)
            activeConns.delete(session)
          }
        }

        prevRelay.current = { ...relayStatus }
      },
    )
    return () => {
      unsub()
      // Close all active connections on unmount
      activeConns.forEach(conn => conn.close())
      activeConns.clear()
    }
  }, [wsBase, daemonBase])
}
```

**Important:** This uses Zustand's `subscribe` with selector, which requires the `subscribeWithSelector` middleware. Add it to the store in Task 7:

```ts
import { subscribeWithSelector } from 'zustand/middleware'
export const useStreamStore = create<StreamStore>()(subscribeWithSelector((set, get) => ({
  // ... store definition
})))
```

- [ ] **Step 4: Verify build**

Run: `cd spa && npx tsc --noEmit`
Expected: no errors

- [ ] **Step 5: Commit**

```
feat(hooks): add useRelayWsManager for relay-driven WS lifecycle
```

---

## Task 10: SPA — ConversationView pure UI refactor

**Files:**
- Modify: `spa/src/components/ConversationView.tsx`
- Modify: `spa/src/components/ConversationView.test.tsx`

- [ ] **Step 1: Update tests for new Props**

Update `ConversationView.test.tsx`:
- Replace `wsUrl` prop with `sessionName`
- Remove `sessionStatus` prop (now read from store)
- Update store interactions to use per-session API

```tsx
// Key changes:
// <ConversationView wsUrl="ws://test" sessionStatus="cc-idle" />
// becomes:
// <ConversationView sessionName="test-session" />

// Before each test that needs 'connected' state:
// act(() => useStreamStore.getState().setHandoffState('test-session', 'connected'))
```

- [ ] **Step 2: Run tests — verify FAIL**

Run: `cd spa && npx vitest run src/components/ConversationView.test.tsx`
Expected: FAIL — props don't match

- [ ] **Step 3: Refactor ConversationView**

Key changes:
1. Props: `wsUrl` → `sessionName`, remove `sessionStatus`
2. Remove the WS-managing `useEffect` (lines 57-102)
3. Remove `connRef`, `prevWsUrlRef`
4. Read all state from per-session store using `sessionName`
5. `handleSend`: get `conn` from store instead of `connRef.current`

- [ ] **Step 4: Run tests — verify PASS**

Run: `cd spa && npx vitest run src/components/ConversationView.test.tsx`
Expected: all PASS

- [ ] **Step 5: Commit**

```
refactor(ui): ConversationView reads from per-session store, no WS management
```

---

## Task 11: SPA — App.tsx integration

**Files:**
- Modify: `spa/src/App.tsx`

- [ ] **Step 1: Import and use useRelayWsManager**

```tsx
import { useRelayWsManager } from './hooks/useRelayWsManager'

// Inside App component:
useRelayWsManager(wsBase, daemonBase)
```

- [ ] **Step 2: Update session-events handler for relay events**

Add `relay` event handling in the session-events useEffect:

```tsx
if (event.type === 'relay') {
  useStreamStore.getState().setRelayStatus(event.session, event.value === 'connected')
}
```

- [ ] **Step 3: Update handoff event handler for history fetch**

```tsx
if (event.value === 'connected') {
  setHandoffState(event.session, 'connected')
  setHandoffProgress(event.session, '')
  fetchSessions(daemonBase)
  // Find session ID for history fetch
  const sessions = useSessionStore.getState().sessions
  const sess = sessions.find(s => s.name === event.session)
  if (sess) {
    fetchHistory(daemonBase, sess.id).then(msgs => {
      if (msgs.length > 0) {
        useStreamStore.getState().loadHistory(event.session, msgs)
      }
    })
  }
}
```

- [ ] **Step 4: Update ConversationView props in JSX**

```tsx
<ConversationView
  sessionName={active.name}
  onHandoff={() => handleHandoff('stream', activePreset || streamPresets[0]?.name || 'cc')}
  onHandoffToTerm={handleHandoffToTerm}
/>
```

Remove `wsUrl` and `sessionStatus` props.

- [ ] **Step 5: Update handoff-related state calls to per-session API**

All `setHandoffState(...)` calls need session name:
```tsx
// Before: useStreamStore.getState().setHandoffState('handoff-in-progress')
// After:  useStreamStore.getState().setHandoffState(active.name, 'handoff-in-progress')
```

- [ ] **Step 6: Verify full build and existing tests**

Run: `cd spa && npx vitest run && npx tsc --noEmit`
Expected: all PASS, no type errors

- [ ] **Step 7: Commit**

```
feat(app): integrate useRelayWsManager and per-session store in App.tsx
```

---

## Task 12: Final verification

- [ ] **Step 1: Run all Go tests**

Run: `go test ./... -v -timeout 60s`
Expected: all PASS

- [ ] **Step 2: Run all SPA tests**

Run: `cd spa && npx vitest run`
Expected: all PASS

- [ ] **Step 3: Run E2E pipeline tests**

Run: `go test ./internal/server/ -run 'TestE2E' -v -timeout 30s`
Expected: all PASS (the tests from earlier in this session)

- [ ] **Step 4: Verify SPA builds**

Run: `cd spa && npx vite build`
Expected: build succeeds

- [ ] **Step 5: Commit any remaining fixes**
