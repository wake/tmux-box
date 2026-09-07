package session

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

func newWatcherTestModule(t *testing.T) (*SessionModule, *tmux.FakeExecutor, *core.EventsBroadcaster) {
	t.Helper()
	meta, err := store.OpenMeta(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { meta.Close() })

	fake := tmux.NewFakeExecutor()
	mod := NewSessionModule(meta)
	c := core.New(core.CoreDeps{
		Tmux:     fake,
		Registry: core.NewServiceRegistry(),
	})
	require.NoError(t, mod.Init(c))
	return mod, fake, c.Events
}

func TestWatcherTmuxAliveInitialState(t *testing.T) {
	mod, _, _ := newWatcherTestModule(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, mod.Start(ctx))
	assert.True(t, mod.TmuxAlive(), "tmux should be alive when FakeExecutor default alive=true")
}

func TestWatcherTransitionsToTmuxDown(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	sub := events.AddTestSubscriber()
	defer events.RemoveTestSubscriber(sub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, mod.Start(ctx))

	fake.SetAlive(false)
	mod.checkAndBroadcast()
	assert.False(t, mod.TmuxAlive())

	select {
	case msg := <-sub.SendCh():
		assert.Contains(t, string(msg), `"type":"tmux"`)
		assert.Contains(t, string(msg), `"value":"unavailable"`)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected tmux unavailable broadcast")
	}
}

func TestWatcherRecoverFromTmuxDown(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	sub := events.AddTestSubscriber()
	defer events.RemoveTestSubscriber(sub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, mod.Start(ctx))

	fake.SetAlive(false)
	mod.checkAndBroadcast()
	assert.False(t, mod.TmuxAlive())
	<-sub.SendCh()

	fake.SetAlive(true)
	fake.AddSession("recovered", "/tmp")
	mod.checkAndBroadcast()
	assert.True(t, mod.TmuxAlive())

	select {
	case msg := <-sub.SendCh():
		assert.Contains(t, string(msg), `"type":"tmux"`)
		assert.Contains(t, string(msg), `"value":"ok"`)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected tmux ok broadcast")
	}
}

func TestWatcherNilSessionsWithTmuxAlive(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	sub := events.AddTestSubscriber()
	defer events.RemoveTestSubscriber(sub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fake.SetAlive(true)
	require.NoError(t, mod.Start(ctx))

	mod.checkAndBroadcast()
	assert.True(t, mod.TmuxAlive())
}

// TestBroadcastSessionsDebounce verifies that rapid concurrent calls to
// broadcastSessions() within the 500ms window result in only one broadcast.
func TestBroadcastSessionsDebounce(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	sub := events.AddTestSubscriber()
	defer events.RemoveTestSubscriber(sub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fake.AddSession("s1", "/tmp")
	require.NoError(t, mod.Start(ctx))

	// Call broadcastSessions twice back-to-back within the debounce window.
	mod.broadcastSessions()
	mod.broadcastSessions()

	// Only one broadcast should have been sent.
	count := 0
	timeout := time.After(100 * time.Millisecond)
drain:
	for {
		select {
		case msg := <-sub.SendCh():
			if len(msg) > 0 {
				count++
			}
		case <-timeout:
			break drain
		}
	}
	assert.Equal(t, 1, count, "debounce should suppress second broadcast within 500ms window")
}

// TestBroadcastSessionsDebounceExpiry verifies that a second call after the
// debounce window has passed DOES produce a broadcast.
func TestBroadcastSessionsDebounceExpiry(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	sub := events.AddTestSubscriber()
	defer events.RemoveTestSubscriber(sub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fake.AddSession("s1", "/tmp")
	require.NoError(t, mod.Start(ctx))

	// First call sets the lastBroadcast timestamp.
	mod.broadcastSessions()

	// Drain first broadcast.
	select {
	case <-sub.SendCh():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected first broadcast")
	}

	// Manually expire the debounce window by backdating lastBroadcast.
	mod.wstate.mu.Lock()
	mod.wstate.lastBroadcast = mod.wstate.lastBroadcast.Add(-600 * time.Millisecond)
	mod.wstate.mu.Unlock()

	// Second call after window expiry should go through.
	mod.broadcastSessions()

	select {
	case msg := <-sub.SendCh():
		assert.Contains(t, string(msg), `"type":"sessions"`, "second broadcast should contain sessions event")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected second broadcast after debounce expiry")
	}
}

func TestWatcherNoRepeatBroadcastInTmuxDown(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	sub := events.AddTestSubscriber()
	defer events.RemoveTestSubscriber(sub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, mod.Start(ctx))

	fake.SetAlive(false)
	mod.checkAndBroadcast()
	<-sub.SendCh()

	mod.checkAndBroadcast()

	select {
	case <-sub.SendCh():
		t.Fatal("should not broadcast tmux unavailable twice in a row")
	case <-time.After(50 * time.Millisecond):
	}
}

// TestTickNormal_InvalidatesNameCacheOnHashChange verifies that when the
// watcher's polling loop detects a session-list change (hash diff), it
// invalidates the LookupCodeByName cache. This is the safety net for the
// case where an external `tmux rename-session` mutates state without going
// through the daemon's HTTP handlers.
func TestTickNormal_InvalidatesNameCacheOnHashChange(t *testing.T) {
	mod, fake, _ := newWatcherTestModule(t)

	fake.AddSession("alpha", "/tmp")

	// Prime tickNormal so its lastHash matches the current session list,
	// otherwise the very first tick we run below would already see a hash
	// change from "" → some-hash and invalidate, masking the real assertion.
	mod.tickNormal()

	// Pre-populate the name cache.
	_, ok := mod.LookupCodeByName("alpha")
	require.True(t, ok)
	mod.nameCacheMu.Lock()
	require.False(t, mod.nameCacheAt.IsZero(), "cache must be populated before the test")
	mod.nameCacheMu.Unlock()

	// Mutate session list externally so the next tickNormal sees a new hash.
	fake.AddSession("beta", "/tmp")

	mod.tickNormal()

	mod.nameCacheMu.Lock()
	defer mod.nameCacheMu.Unlock()
	assert.True(t, mod.nameCacheAt.IsZero(), "tickNormal must invalidate name cache when hash changes")
}

// TestTickNormal_DoesNotInvalidateOnUnchangedHash verifies that the steady
// state (no session changes) does not pointlessly bust the cache.
func TestTickNormal_DoesNotInvalidateOnUnchangedHash(t *testing.T) {
	mod, fake, _ := newWatcherTestModule(t)

	fake.AddSession("alpha", "/tmp")

	// First tick to register the current hash.
	mod.tickNormal()

	// Pre-populate the name cache.
	_, ok := mod.LookupCodeByName("alpha")
	require.True(t, ok)
	mod.nameCacheMu.Lock()
	require.False(t, mod.nameCacheAt.IsZero(), "cache must be populated before the test")
	mod.nameCacheMu.Unlock()

	// No mutation: the next tick must see an identical hash and not invalidate.
	mod.tickNormal()

	mod.nameCacheMu.Lock()
	defer mod.nameCacheMu.Unlock()
	assert.False(t, mod.nameCacheAt.IsZero(), "tickNormal must not invalidate name cache when hash is unchanged")
}

// TestBroadcastSessions_InvalidatesNameCache verifies that the wait-for path
// (which lands in broadcastSessions) invalidates the name cache. This is the
// authoritative signal — tmux just told us "something changed".
func TestBroadcastSessions_InvalidatesNameCache(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	sub := events.AddTestSubscriber()
	defer events.RemoveTestSubscriber(sub)

	fake.AddSession("alpha", "/tmp")

	// Pre-populate the name cache.
	_, ok := mod.LookupCodeByName("alpha")
	require.True(t, ok)
	mod.nameCacheMu.Lock()
	require.False(t, mod.nameCacheAt.IsZero(), "cache must be populated before the test")
	mod.nameCacheMu.Unlock()

	mod.broadcastSessions()

	mod.nameCacheMu.Lock()
	defer mod.nameCacheMu.Unlock()
	assert.True(t, mod.nameCacheAt.IsZero(), "broadcastSessions must invalidate name cache")
}

func TestHashSessionsChangesWhenPaneTitleChanges(t *testing.T) {
	base := []SessionInfo{{
		Code:      "aa",
		TmuxID:    "$0",
		Name:      "dev",
		Exists:    true,
		Mode:      "terminal",
		Cwd:       "/tmp",
		PaneTitle: "first title",
	}}
	changed := []SessionInfo{{
		Code:      "aa",
		TmuxID:    "$0",
		Name:      "dev",
		Exists:    true,
		Mode:      "terminal",
		Cwd:       "/tmp",
		PaneTitle: "second title",
	}}

	assert.NotEqual(t, hashSessions("i", base), hashSessions("i", changed))
}

// drainSessions collects the inner JSON payload of every "sessions" event the
// subscriber received. Frames on SendCh() are the outer core.HostEvent
// envelope, so the type is checked before the value is kept.
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

func TestGetSession_StampsTmuxInstance(t *testing.T) {
	mod, fake, _ := newWatcherTestModule(t)
	fake.AddSession("dev", "/w")
	mod.tmuxInstanceFn = func() string { return "333:3000" }

	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	require.NotEmpty(t, sessions)

	info, err := mod.GetSession(sessions[0].Code)
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, "333:3000", info.TmuxInstance, "the single-get path must stamp the generation too")
}

func TestTmuxInstance_ProviderMethodSamplesEveryCall(t *testing.T) {
	mod, _, _ := newWatcherTestModule(t)
	calls := 0
	mod.tmuxInstanceFn = func() string {
		calls++
		return "444:4000"
	}
	assert.Equal(t, "444:4000", mod.TmuxInstance())
	assert.Equal(t, "444:4000", mod.TmuxInstance())
	assert.Equal(t, 2, calls, "every call must re-sample rather than reuse a cached value")
}
