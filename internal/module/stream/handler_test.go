package stream

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/bridge"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/session"
)

// fakeSessionProvider implements session.SessionProvider for testing.
type fakeSessionProvider struct {
	mu       sync.Mutex
	sessions map[string]*session.SessionInfo
	updates  []metaUpdateRecord
}

type metaUpdateRecord struct {
	Code   string
	Update session.MetaUpdate
}

func (f *fakeSessionProvider) ListSessions() ([]session.SessionInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []session.SessionInfo
	for _, s := range f.sessions {
		out = append(out, *s)
	}
	return out, nil
}

func (f *fakeSessionProvider) GetSession(code string) (*session.SessionInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.sessions[code]; ok {
		return s, nil
	}
	return nil, nil
}

func (f *fakeSessionProvider) UpdateMeta(code string, update session.MetaUpdate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, metaUpdateRecord{Code: code, Update: update})
	// Apply the update to the in-memory session
	if s, ok := f.sessions[code]; ok {
		if update.Mode != nil {
			s.Mode = *update.Mode
		}
		if update.CCModel != nil {
			s.CCModel = *update.CCModel
		}
		if update.CCSessionID != nil {
			s.CCSessionID = *update.CCSessionID
		}
		if update.Cwd != nil {
			s.Cwd = *update.Cwd
		}
	}
	return nil
}

func (f *fakeSessionProvider) HandleTerminalWS(w http.ResponseWriter, r *http.Request, code string) {
}

func (f *fakeSessionProvider) TmuxInstance() string { return "" }

func (f *fakeSessionProvider) getUpdates() []metaUpdateRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]metaUpdateRecord, len(f.updates))
	copy(out, f.updates)
	return out
}

// setupStreamModule creates a StreamModule wired with fakes for testing.
// Returns the module, the fake provider, and an httptest.Server.
func setupStreamModule(t *testing.T, sessions map[string]*session.SessionInfo) (*StreamModule, *fakeSessionProvider, *httptest.Server) {
	t.Helper()

	fp := &fakeSessionProvider{sessions: sessions}

	m := &StreamModule{
		core:     &core.Core{Events: core.NewEventsBroadcaster()},
		bridge:   bridge.New(),
		sessions: fp,
		locks:    newHandoffLocks(),
	}

	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return m, fp, srv
}

func wsURL(srv *httptest.Server, path string) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http") + path
}

func dial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", url, err)
	}
	if resp.Body != nil {
		resp.Body.Close()
	}
	return conn
}

// --- handleCliBridge tests ---

func TestCliBridge_RelayAndSubscriber(t *testing.T) {
	sessions := map[string]*session.SessionInfo{
		"abc123": {Code: "abc123", Name: "test-sess", Mode: "stream"},
	}
	_, _, srv := setupStreamModule(t, sessions)

	// Connect relay
	relay := dial(t, wsURL(srv, "/ws/cli-bridge/abc123"))
	defer relay.Close()
	time.Sleep(50 * time.Millisecond)

	// Connect subscriber
	sub := dial(t, wsURL(srv, "/ws/cli-bridge-sub/abc123"))
	defer sub.Close()

	// Relay sends → subscriber receives
	relay.WriteMessage(websocket.TextMessage, []byte(`{"type":"assistant","content":"hello"}`))
	sub.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := sub.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, `{"type":"assistant","content":"hello"}`, string(msg))

	// Subscriber sends → relay receives
	sub.WriteMessage(websocket.TextMessage, []byte(`{"type":"user","content":"hi"}`))
	relay.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err = relay.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, `{"type":"user","content":"hi"}`, string(msg))
}

func TestCliBridge_FanOutMultipleSubscribers(t *testing.T) {
	sessions := map[string]*session.SessionInfo{
		"fan123": {Code: "fan123", Name: "fanout", Mode: "stream"},
	}
	_, _, srv := setupStreamModule(t, sessions)

	relay := dial(t, wsURL(srv, "/ws/cli-bridge/fan123"))
	defer relay.Close()
	time.Sleep(50 * time.Millisecond)

	sub1 := dial(t, wsURL(srv, "/ws/cli-bridge-sub/fan123"))
	defer sub1.Close()
	sub2 := dial(t, wsURL(srv, "/ws/cli-bridge-sub/fan123"))
	defer sub2.Close()

	relay.WriteMessage(websocket.TextMessage, []byte(`{"data":"broadcast"}`))

	for i, sub := range []*websocket.Conn{sub1, sub2} {
		sub.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, msg, err := sub.ReadMessage()
		require.NoError(t, err, "sub%d read", i+1)
		assert.Equal(t, `{"data":"broadcast"}`, string(msg), "sub%d message", i+1)
	}
}

func TestCliBridge_DuplicateRelay_Returns409(t *testing.T) {
	sessions := map[string]*session.SessionInfo{
		"dup123": {Code: "dup123", Name: "dup-sess", Mode: "stream"},
	}
	_, _, srv := setupStreamModule(t, sessions)

	relay := dial(t, wsURL(srv, "/ws/cli-bridge/dup123"))
	defer relay.Close()
	time.Sleep(50 * time.Millisecond)

	// Second relay attempt → 409
	resp, err := http.Get(srv.URL + "/ws/cli-bridge/dup123")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
}

func TestCliBridge_SessionNotFound_Returns404(t *testing.T) {
	// Empty sessions map — no sessions exist
	_, _, srv := setupStreamModule(t, map[string]*session.SessionInfo{})

	resp, err := http.Get(srv.URL + "/ws/cli-bridge/nope00")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestCliBridge_BroadcastsRelayConnectedAndDisconnected(t *testing.T) {
	sessions := map[string]*session.SessionInfo{
		"evt123": {Code: "evt123", Name: "evt-sess", Mode: "stream"},
	}
	m, _, srv := setupStreamModule(t, sessions)

	// Add test subscriber to events
	eventSub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(eventSub)

	relay := dial(t, wsURL(srv, "/ws/cli-bridge/evt123"))

	// Should receive "relay connected" event
	select {
	case msg := <-eventSub.SendCh():
		var evt core.HostEvent
		require.NoError(t, json.Unmarshal(msg, &evt))
		assert.Equal(t, "relay", evt.Type)
		assert.Equal(t, "evt123", evt.Session)
		assert.Equal(t, "connected", evt.Value)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for relay connected event")
	}

	// Close relay → should receive "relay disconnected" event
	relay.Close()

	select {
	case msg := <-eventSub.SendCh():
		var evt core.HostEvent
		require.NoError(t, json.Unmarshal(msg, &evt))
		assert.Equal(t, "relay", evt.Type)
		assert.Equal(t, "evt123", evt.Session)
		assert.Equal(t, "disconnected", evt.Value)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for relay disconnected event")
	}
}

func TestCliBridge_InitMetadataCapture(t *testing.T) {
	sessions := map[string]*session.SessionInfo{
		"ini123": {Code: "ini123", Name: "init-sess", Mode: "stream"},
	}
	_, fp, srv := setupStreamModule(t, sessions)

	relay := dial(t, wsURL(srv, "/ws/cli-bridge/ini123"))
	defer relay.Close()
	time.Sleep(50 * time.Millisecond)

	// Relay sends init message
	relay.WriteMessage(websocket.TextMessage, []byte(
		`{"type":"system","subtype":"init","model":"claude-opus-4-6","session_id":"xyz"}`,
	))
	time.Sleep(200 * time.Millisecond)

	// Verify UpdateMeta was called with the model
	updates := fp.getUpdates()
	require.NotEmpty(t, updates, "expected UpdateMeta call for init metadata")

	found := false
	for _, u := range updates {
		if u.Code == "ini123" && u.Update.CCModel != nil && *u.Update.CCModel == "claude-opus-4-6" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected UpdateMeta with CCModel=claude-opus-4-6, got %+v", updates)
}

func TestCliBridge_ModeRevertOnRelayDisconnect(t *testing.T) {
	sessions := map[string]*session.SessionInfo{
		"rev123": {Code: "rev123", Name: "revert-sess", Mode: "stream"},
	}
	m, fp, srv := setupStreamModule(t, sessions)

	eventSub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(eventSub)

	relay := dial(t, wsURL(srv, "/ws/cli-bridge/rev123"))
	// Drain the "connected" event
	select {
	case <-eventSub.SendCh():
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for connected event")
	}

	// Close relay — triggers mode revert
	relay.Close()
	time.Sleep(200 * time.Millisecond)

	// Verify mode was reverted to "terminal"
	updates := fp.getUpdates()
	found := false
	for _, u := range updates {
		if u.Code == "rev123" && u.Update.Mode != nil && *u.Update.Mode == "terminal" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected mode revert to terminal, got %+v", updates)
}

// --- handleCliBridgeSubscribe tests ---

func TestCliBridgeSubscribe_NoRelay_Returns404(t *testing.T) {
	_, _, srv := setupStreamModule(t, map[string]*session.SessionInfo{
		"sub123": {Code: "sub123", Name: "sub-sess", Mode: "stream"},
	})

	// No relay connected — subscribe should 404
	resp, err := http.Get(srv.URL + "/ws/cli-bridge-sub/sub123")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestCliBridgeSubscribe_ReceivesRelayedMessages(t *testing.T) {
	sessions := map[string]*session.SessionInfo{
		"msg123": {Code: "msg123", Name: "msg-sess", Mode: "stream"},
	}
	_, _, srv := setupStreamModule(t, sessions)

	relay := dial(t, wsURL(srv, "/ws/cli-bridge/msg123"))
	defer relay.Close()
	time.Sleep(50 * time.Millisecond)

	sub := dial(t, wsURL(srv, "/ws/cli-bridge-sub/msg123"))
	defer sub.Close()

	// Send multiple messages
	for i, payload := range []string{`{"msg":1}`, `{"msg":2}`, `{"msg":3}`} {
		relay.WriteMessage(websocket.TextMessage, []byte(payload))
		sub.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, msg, err := sub.ReadMessage()
		require.NoError(t, err, "message %d", i)
		assert.Equal(t, payload, string(msg), "message %d", i)
	}
}

// --- handleHandoff validation test ---

func TestHandoff_NilBody_Returns400(t *testing.T) {
	_, _, srv := setupStreamModule(t, map[string]*session.SessionInfo{
		"hnd123": {Code: "hnd123", Name: "handoff-sess", Mode: "terminal"},
	})

	resp, err := http.Post(srv.URL+"/api/sessions/hnd123/handoff", "application/json", nil)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// --- sendRelaySnapshot test ---

func TestSendRelaySnapshot(t *testing.T) {
	sessions := map[string]*session.SessionInfo{
		"snp123": {Code: "snp123", Name: "snap-sess", Mode: "stream"},
	}
	m, _, srv := setupStreamModule(t, sessions)

	// Connect a relay so the bridge has an active session
	relay := dial(t, wsURL(srv, "/ws/cli-bridge/snp123"))
	defer relay.Close()
	time.Sleep(50 * time.Millisecond)

	// Create a test event subscriber and call sendRelaySnapshot
	eventSub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(eventSub)

	m.sendRelaySnapshot(eventSub)

	select {
	case msg := <-eventSub.SendCh():
		var evt core.HostEvent
		require.NoError(t, json.Unmarshal(msg, &evt))
		assert.Equal(t, "relay", evt.Type)
		assert.Equal(t, "snp123", evt.Session)
		assert.Equal(t, "connected", evt.Value)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for relay snapshot")
	}
}

// --- handoffLocks tests ---

func TestHandoffLocks_TryLockAndUnlock(t *testing.T) {
	locks := newHandoffLocks()

	assert.True(t, locks.TryLock("abc"), "first lock should succeed")
	assert.False(t, locks.TryLock("abc"), "second lock should fail")

	locks.Unlock("abc")
	assert.True(t, locks.TryLock("abc"), "lock after unlock should succeed")
}

func TestHandoffLocks_IndependentKeys(t *testing.T) {
	locks := newHandoffLocks()

	assert.True(t, locks.TryLock("a"))
	assert.True(t, locks.TryLock("b"))
	assert.False(t, locks.TryLock("a"))
	assert.True(t, locks.TryLock("c"))
}
