package session

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/tmux"
)

// The window this closes (spec §4.6.2):
//
// `handleSendKeys` sampled the generation inside `GetSession` and then compared
// it. Between that sample and the keystrokes sat `ActivePaneMetadata` — several
// tmux subprocesses — and a DB read, and the send itself then opened a NEW tmux
// connection and resolved the target by NAME. A restart in that window passed
// the check and delivered the keys to the new server.
//
// Re-sampling closer to the send does not fix it: any check that is a separate
// tmux invocation from the send has the same window, only narrower. The
// condition is therefore evaluated by the server that performs the send, in one
// invocation, and the daemon acts on THAT verdict.
func TestHandlerSendKeys_ServerRestartsDuringMetadataRead_RefusesWithoutSending(t *testing.T) {
	mod, _, fake := newTestModule(t)
	// The daemon reads its generation from the same server the executor talks
	// to, so one `SetInstance` moves the whole world at once.
	mod.tmuxInstanceFn = fake.Instance
	fake.SetInstance("111:1000")
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	fake.AddSession("target", "/tmp")
	fake.SetActivePaneMetadata("target", tmux.TmuxPaneMetadata{SessionID: "$0", SessionName: "target"})

	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	code := sessions[0].Code

	// The restart lands exactly where the old code was blind: after the
	// generation was sampled, before the keys go out. `$0` is minted again, so
	// the recorded code still resolves — to a stranger.
	fake.SetActivePaneMetadataHook(func(string) { fake.SetInstance("222:2000") })

	w := sendKeysTo(t, mux, code, `{"keys":"claude --resume S1\n","expected_tmux_instance":"111:1000"}`)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Empty(t, fake.RawKeysSent(), "not one keystroke may reach the new server")
}

// The same window, with the restart landing on a generation the caller WOULD
// have accepted had the daemon only re-sampled: the point is that the daemon
// never decides on its own sample at all.
func TestHandlerSendKeys_MatchingGeneration_SendsBySessionID(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mod.tmuxInstanceFn = fake.Instance
	fake.SetInstance("111:1000")
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	fake.AddSession("target", "/tmp")
	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	code := sessions[0].Code

	w := sendKeysTo(t, mux, code, `{"keys":"claude -c\n","expected_tmux_instance":"111:1000"}`)

	require.Equal(t, http.StatusNoContent, w.Code)
	calls := fake.RawKeysSent()
	require.Len(t, calls, 1)
	// By ID, not by name: a rename cannot re-point a session id, and the id is
	// what the code decodes to in the first place.
	assert.Equal(t, "$0:", calls[0].Target)
	assert.Equal(t, []string{"claude -c\n"}, calls[0].Keys)
}

// A tmux that cannot answer at all is not a refusal — the caller learns the
// request failed rather than being told its expectation was wrong — and it
// still sends nothing.
func TestHandlerSendKeys_ConditionalSendFails_Reports500AndSendsNothing(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mod.tmuxInstanceFn = fake.Instance
	fake.SetInstance("111:1000")
	fake.FailSendKeys = true
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	fake.AddSession("target", "/tmp")
	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	code := sessions[0].Code

	w := sendKeysTo(t, mux, code, `{"keys":"claude -c\n","expected_tmux_instance":"111:1000"}`)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Empty(t, fake.RawKeysSent())
}

// A malformed expectation is a bad request, not a refusal: it could not be
// asserted against any generation, and it must never reach a tmux format.
func TestHandlerSendKeys_UnsafeExpectation_Refuses400(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mod.tmuxInstanceFn = fake.Instance
	fake.SetInstance("111:1000")
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	fake.AddSession("target", "/tmp")
	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	code := sessions[0].Code

	w := sendKeysTo(t, mux, code, `{"keys":"claude -c\n","expected_tmux_instance":"1,1}#{==:1,1"}`)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Empty(t, fake.RawKeysSent())
}
