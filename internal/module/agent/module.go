// Package agent provides the agent hook event module.
// It receives hook events from `pdx hook`, projects live state into frames,
// and broadcasts to WS subscribers. AgentEventStore remains as a legacy
// fallback source for pre-frame sessions and session rename compatibility.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	agentcc "github.com/wake/purdex/internal/agent/cc"
	"github.com/wake/purdex/internal/agent/codex"
	"github.com/wake/purdex/internal/agent/opencode"
	"github.com/wake/purdex/internal/agent/probe"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

// Module is the agent hook event module.
type Module struct {
	core      *core.Core
	events    *store.AgentEventStore
	frames    *store.FramesStore
	traces    *store.TraceStore
	sessions  session.SessionProvider
	registry  *agentpkg.Registry
	uploadDir string
	traceSink *hookTraceSink

	prober    *probe.Prober
	probeOrch *probeOrchestrator
	tmux      tmux.Executor

	mu             sync.Mutex
	currentStatus  map[string]agentpkg.Status
	subagents      map[string][]agentpkg.SubagentRef
	activeWatchers map[string]string // tmuxSession → agentType

	// W6-3 P1-T4: ProbeIntent dispatcher state. activeProbeIntents and
	// probeIntentGen are protected by m.mu (same mutex as activeWatchers).
	// probeIntentDisp is the long-lived dispatcher pointer; created in New().
	activeProbeIntents map[string]map[agentpkg.ProbeIntentKind]activeIntent
	probeIntentGen     uint64
	probeIntentDisp    *probeIntentDispatcher

	// statusSnapshots caches the latest statusline payload per sessionCode.
	// Display-only, not persisted; guarded by snapshotMu (separate from mu
	// because hot-path agent.status POSTs shouldn't contend with hook writes).
	snapshotMu      sync.RWMutex
	statusSnapshots map[string]statusSnapshot

	// testObservers: per-nonce channel for the statusline self-test endpoint.
	// Guarded by testMu (separate from snapshotMu and mu so test traffic
	// cannot block production hook / status writes).
	testMu        sync.Mutex
	testObservers map[string]*testObserver

	// testSpawnProxy is a test seam; production leaves this nil so the handler
	// falls back to defaultSpawnTestProxy which execs the real pdx binary.
	testSpawnProxy func(nonce string) error

	sweepCancel context.CancelFunc
	sweepWG     sync.WaitGroup

	pathHintDedup  *PathHintDedupCache
	pathHintBuffer *PathHintRingBuffer
}

// Test seams for Module.New. framesInitFn failure is fatal (hook processing
// depends on frames); tracesInitFn failure is best-effort (trace
// observability degrades to nil, daemon continues).
var (
	framesInitFn = func(e *store.AgentEventStore) (*store.FramesStore, error) { return e.Frames() }
	tracesInitFn = func(e *store.AgentEventStore) (*store.TraceStore, error) { return e.Traces() }
)

// New creates a new agent Module backed by the given AgentEventStore.
//
// Returns an error ONLY when the frames store cannot be initialized —
// typically when on-disk agent_frames contains malformed subagents_json
// that migrateFramesDB refuses to classify. That failure is fatal because
// hook processing depends on m.frames; continuing with m.frames == nil
// would degrade silently via the module's m.frames == nil fallbacks.
//
// Trace store initialization is best-effort: the module already tolerates
// m.traces == nil / m.traceSink == nil in normal operation (monitor
// endpoints degrade, hook processing still runs). A trace-table migration
// or corruption error is logged and the module continues without trace
// observability, not treated as a daemon-fatal condition.
func New(events *store.AgentEventStore) (*Module, error) {
	var frames *store.FramesStore
	var traces *store.TraceStore
	if events != nil {
		var err error
		frames, err = framesInitFn(events)
		if err != nil {
			return nil, fmt.Errorf("agent module: frames store: %w", err)
		}
		traces, err = tracesInitFn(events)
		if err != nil {
			log.Printf("[agent] traces store unavailable, continuing without trace observability: %v", err)
			traces = nil
		}
	}
	m := &Module{
		events:             events,
		frames:             frames,
		traces:             traces,
		registry:           agentpkg.NewRegistry(),
		currentStatus:      make(map[string]agentpkg.Status),
		subagents:          make(map[string][]agentpkg.SubagentRef),
		activeWatchers:     make(map[string]string),
		activeProbeIntents: make(map[string]map[agentpkg.ProbeIntentKind]activeIntent),
		statusSnapshots:    make(map[string]statusSnapshot),
		testObservers:      make(map[string]*testObserver),
		pathHintDedup:      NewPathHintDedupCache(5 * time.Second),
		pathHintBuffer:     NewPathHintRingBuffer(200),
	}
	if traces != nil {
		m.traceSink = newHookTraceSink(traces)
	}
	// probeOrchestrator owns probe-watcher lifecycle. Created here (not in
	// Init) so it is always available for tests that bypass Init and assign
	// m.prober directly. The orchestrator resolves m.prober lazily, so
	// "AFTER prober is set" semantics hold at startWatch call time.
	m.probeOrch = newProbeOrchestrator(m)
	// W6-3 P1-T4: ProbeIntent dispatcher. Created here (not in Init) so the
	// pointer is non-nil throughout module lifetime; tests that bypass Init
	// can call manageActivityWatch / replay paths without nil-checking.
	// parentCtx defaults to context.Background; rotated in Stop().
	m.probeIntentDisp = newProbeIntentDispatcher(m)
	// W6-3 P2-T4: route declared ProbeIntent kinds to their per-agent detectors.
	// The closure resolves m.tmux lazily so it picks up Init()'s wiring (m.tmux
	// is nil at New() time and assigned during Init); callers (lifecycle plan)
	// only invoke startDetector after applyIntentLifecycle has read top-frame
	// state, by which point Init has long completed in production.
	//
	// Per spec §5.4 lines 776-780: dispatcher switches on Kind; future Kinds
	// add cases here. Unknown Kind falls through to defaultStartProbeIntentDetector
	// (waits on ctx, never emits) — defensive landing for a Kind that surfaces
	// in registry before its detector lands.
	m.probeIntentDisp.startDetector = func(ctx context.Context, mod *Module, kind agentpkg.ProbeIntentKind, paneID string, senderPID int, out chan<- agentpkg.Signal) {
		switch kind {
		case agentpkg.ProbeIntentKindProcessDead:
			codex.StartProcessDeadDetector(ctx, mod.tmux, paneID, senderPID, out)
		case agentpkg.ProbeIntentKindScreenChange:
			// W6-6: codex permission-approval recovery. The detector
			// observes top-10 lines of the pane via mod.prober.Watch
			// and emits one Signal once Phase A (ScreenStable) +
			// Phase B (ScreenChanged) complete with a verified codex
			// identity.
			//
			// isCodexAlive resolves identity via FirstAliveAgentInTree
			// rather than IsAliveFor: FirstAliveAgentInTree internally
			// uses tmux ActivePanePID(target) which honors paneID `%N`
			// targets exactly, while IsAliveFor's PanePID resolves a
			// pane id target to the FIRST pane of its window — wrong
			// for non-first siblings. The IsAliveFor inconsistency is
			// pre-existing infrastructure tracked in a follow-up issue
			// (W6-6 spec §11 line 744).
			isCodexAlive := func() bool {
				t, _, err := mod.prober.FirstAliveAgentInTree(paneID)
				return err == nil && t == "codex"
			}
			codex.StartScreenChangeDetector(ctx, mod.prober, isCodexAlive, paneID, senderPID, out)
		default:
			defaultStartProbeIntentDetector(ctx, mod, kind, paneID, senderPID, out)
		}
	}
	// supportedKinds MUST mirror the switch above. Lifecycle fails closed
	// for any kind missing here (audit F6). Adding a new kind requires
	// extending both the switch and this map together; the drift test
	// `TestProbeIntentDrift_AllDeclaredKindsHaveDispatcherCase` enforces parity.
	m.probeIntentDisp.supportedKinds = map[agentpkg.ProbeIntentKind]struct{}{
		agentpkg.ProbeIntentKindProcessDead:  {},
		agentpkg.ProbeIntentKindScreenChange: {},
	}
	return m, nil
}

func (m *Module) Name() string           { return "agent" }
func (m *Module) Dependencies() []string { return []string{"session"} }

// Init retrieves the SessionProvider, initializes the provider registry,
// and registers CC and Codex providers.
func (m *Module) Init(c *core.Core) error {
	m.core = c
	svc, ok := c.Registry.Get(session.RegistryKey)
	if !ok {
		log.Printf("[agent] warning: session provider not found")
		return nil
	}
	m.sessions = svc.(session.SessionProvider)

	// Surface whether the hook hot path will use the LookupCodeByName fast
	// path or fall back to the 1+7×S ListSessions fan-out. Anything that
	// wraps SessionProvider with a decorator that drops LookupCodeByName
	// will be visible at startup instead of as a silent latency regression.
	if _, ok := m.sessions.(sessionCodeLookuper); ok {
		log.Print("[agent] hook fast-path active (LookupCodeByName available)")
	} else {
		log.Print("[agent] hook fast-path NOT active — sessions does not implement LookupCodeByName")
	}

	// Expose event store and module so other modules (e.g. session rename) can update it.
	c.Registry.Register("agent.events", m.events)
	c.Registry.Register("agent.module", m)

	if m.uploadDir == "" {
		c.CfgMu.RLock()
		m.uploadDir = c.Cfg.UploadDir
		c.CfgMu.RUnlock()
	}

	// Prober (shared across all providers)
	m.tmux = c.Tmux
	m.prober = probe.New(c.Tmux)
	ccProvider := agentcc.NewProvider(m.prober, c.Tmux, c.Cfg, &c.CfgMu)
	m.prober.RegisterIdentifier(ccProvider.Type(), ccProvider.Identify)
	m.prober.RegisterReadiness(ccProvider.Type(), agentcc.NewReadinessChecker(c.Tmux))
	ccProvider.RegisterServices(c.Registry)
	m.registry.Register(ccProvider)

	codexProvider := codex.NewProvider()
	m.prober.RegisterIdentifier(codexProvider.Type(), codexProvider.Identify)
	m.prober.RegisterReadiness(codexProvider.Type(), codex.NewReadinessChecker(c.Tmux))
	c.Registry.Register("agent.prober", m.prober)

	// Listen for config changes to update mutable module state.
	c.OnConfigChange(func() {
		c.CfgMu.RLock()
		newDir := c.Cfg.UploadDir
		c.CfgMu.RUnlock()
		if newDir != "" {
			m.mu.Lock()
			m.uploadDir = newDir
			m.mu.Unlock()
		}
	})

	// Codex provider
	m.registry.Register(codexProvider)

	opencodeProvider := opencode.NewProvider()
	m.prober.RegisterIdentifier(opencodeProvider.Type(), opencodeProvider.Identify)
	m.registry.Register(opencodeProvider)

	// Expose registry for other modules
	c.Registry.Register("agent.registry", m.registry)

	return nil
}

// RegisterRoutes registers the agent API endpoints.
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/agent/event", m.handleEvent)
	mux.HandleFunc("GET /api/agent/monitor/chains", m.handleMonitorChains)
	mux.HandleFunc("GET /api/agent/monitor/chains/{id}", m.handleMonitorChain)
	mux.HandleFunc("GET /api/agent/monitor/projection", m.handleMonitorProjection)
	mux.HandleFunc("GET /api/hooks/{agent}/status", m.handleHookStatus)
	mux.HandleFunc("POST /api/hooks/{agent}/setup", m.handleHookSetup)
	mux.HandleFunc("GET /api/agent/{agent}/statusline/status", m.handleStatuslineStatus)
	mux.HandleFunc("POST /api/agent/{agent}/statusline/setup", m.handleStatuslineSetup)
	mux.HandleFunc("POST /api/agent/cc/statusline/test", m.handleStatuslineTest)
	mux.HandleFunc("POST /api/agent/cc/statusline/test/ready", m.handleStatuslineTestReady)
	mux.HandleFunc("POST /api/agent/status", m.handleAgentStatus)
	mux.HandleFunc("GET /api/agent/title/status", m.handleTitleStatus)
	mux.HandleFunc("POST /api/agent/title/setup", m.handleTitleSetup)
	mux.HandleFunc("GET /api/agents/detect", m.handleDetect)

	// History (delegates to provider)
	mux.HandleFunc("GET /api/sessions/{code}/history", m.handleHistory)

	// Ownership query: which agent owns this tmux session (spec §5.3)
	mux.HandleFunc("GET /api/sessions/{code}/provenance", m.handleSessionProvenance)

	// Upload (unchanged)
	mux.HandleFunc("POST /api/agent/upload", m.handleUpload)
	mux.HandleFunc("GET /api/upload/stats", m.handleUploadStats)
	mux.HandleFunc("GET /api/upload/files", m.handleUploadFiles)
	mux.HandleFunc("DELETE /api/upload/files/{session}/{filename}", m.handleDeleteUploadFile)
	mux.HandleFunc("DELETE /api/upload/files/{session}", m.handleDeleteUploadSession)
	mux.HandleFunc("DELETE /api/upload/files", m.handleDeleteAllUploads)
}

// Start replays DB state and registers OnSubscribe callback.
//
// W6-3 P1-T6 (closes #698): after replayFromDB hydrates currentStatus +
// frame projection and startSweep is running, replayStatus walks every
// session and re-evaluates ProbeIntent gating so detectors armed before
// shutdown rearm after restart. Sequence MUST be replayFromDB → startSweep
// → replayStatus so detectors see fully-hydrated state on first poll
// (per spec §6.3 / §6.4).
func (m *Module) Start(_ context.Context) error {
	if err := m.sweepOnce(); err != nil {
		log.Printf("[agent] startup sweep: %v", err)
	}
	m.replayFromDB()
	m.startSweep()
	if m.probeIntentDisp != nil {
		m.probeIntentDisp.replayStatus()
	}

	if m.core != nil {
		m.core.Events.OnSubscribe(func(sub *core.EventSubscriber) {
			m.sendSnapshot(sub)
			m.sendStatuslineSnapshot(sub)
		})
	}

	log.Println("[agent] hook event endpoint registered")
	return nil
}

// getUploadDir returns the current upload directory under lock.
func (m *Module) getUploadDir() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.uploadDir
}

// Stop cancels all active Activity watchers and resets transient state.
func (m *Module) Stop(_ context.Context) error {
	if m.sweepCancel != nil {
		m.sweepCancel()
		m.sweepWG.Wait()
		m.sweepCancel = nil
	}
	if m.prober != nil {
		m.prober.StopAllWatches()
	}
	// W6-3 P1-T4: cancel every armed ProbeIntent detector before clearing
	// activeWatchers. stopAll uses the same m.mu, so a concurrent applyStatus
	// either observes the pre-Stop active map or runs to completion — never
	// partial.
	if m.probeIntentDisp != nil {
		m.probeIntentDisp.stopAll()
	}
	m.mu.Lock()
	m.activeWatchers = make(map[string]string)
	m.mu.Unlock()
	if m.traceSink != nil {
		m.traceSink.Close()
	}
	return nil
}

// renameSessionLocked transfers in-memory agent state (subagents, currentStatus,
// activeWatchers, activeProbeIntents) from oldName to newName.  CALLER MUST
// hold m.mu.
//
// Returns a slice of CancelFuncs that the caller MUST invoke AFTER releasing
// m.mu. This routes the W6-3 P1-T5 ProbeIntent cleanup through the same
// post-unlock pattern as dispatchProbeIntentReeval: detector goroutines that
// were keyed under oldName get cancelled outside the lock so any concurrent
// applyProbeGuards re-acquisition does not deadlock. Caller may pass the
// returned slice to dispatchProbeIntentRenameCancels (or invoke each cancel
// directly).
func (m *Module) renameSessionLocked(oldName, newName string) []context.CancelFunc {
	if subs, ok := m.subagents[oldName]; ok {
		m.subagents[newName] = subs
		delete(m.subagents, oldName)
	}
	if status, ok := m.currentStatus[oldName]; ok {
		m.currentStatus[newName] = status
		delete(m.currentStatus, oldName)
	}
	if _, ok := m.activeWatchers[oldName]; ok {
		// W3 撤回: rename is now stop-only. Phase 4a-1 wired a stopWatch +
		// startWatch(newName) sequence to keep the screen-watcher alive across
		// rename, but with manageActivityWatch reduced to a default no-op
		// there is no production caller starting watchers in the first place.
		// W6 will reintroduce starts via ProbeIntent; until then, evict the
		// renamed entry and tear down the orchestrator-side watcher (if any).
		// activeWatchers eviction is m.mu-protected; caller holds m.mu.
		delete(m.activeWatchers, oldName)
		// orchestrator stop is lock-free wrt m.mu (it touches prober.watcherMu,
		// a different mutex) so calling it while we hold m.mu is safe.
		// nil-prober is handled inside stopWatch (R14 fix).
		m.probeOrch.stopWatch(oldName)
		// Codex finding #2 regression (kept under W3): migrate the active
		// graceWindow so a hook-set status that was just recorded under
		// oldName is not overwritten by a probe event arriving for newName
		// within probeGraceWindow once W6 wires starts again.
		m.probeOrch.migrateLastHookAt(oldName, newName)
	}

	// W6-3 P1-T5: drop ProbeIntent active entries keyed under oldName. The
	// dispatcher cannot rearm under newName until our caller invokes
	// dispatcher.applyStatus(newName, ...) post-unlock, so collecting +
	// returning the cancel list (rather than invoking inside the lock) keeps
	// the cancel + arm sequence contiguous from the dispatcher's POV.
	var toCancel []context.CancelFunc
	if perSession, ok := m.activeProbeIntents[oldName]; ok {
		for _, cur := range perSession {
			toCancel = append(toCancel, cur.cancel)
		}
		delete(m.activeProbeIntents, oldName)
	}
	return toCancel
}

// RenameSession transfers in-memory agent state from oldName to newName
// under the module's lock.  Used by callers that don't need to coordinate
// with other rename steps (e.g. tests).  Production callers should prefer
// RenameSessionAtomic to make the rename atomic with tmux + DB updates.
//
// W6-3 P1-T5: after the in-memory rename completes, the ProbeIntent
// dispatcher is re-evaluated for newName so any active codex / future
// per-agent probes stay armed against the renamed session. The lock is
// released before invoking dispatcher.applyStatus because the dispatcher
// itself acquires m.mu (deadlock otherwise).
func (m *Module) RenameSession(oldName, newName string) {
	m.mu.Lock()
	cancels := m.renameSessionLocked(oldName, newName)
	plan, hasPlan := m.captureProbeIntentReevalLocked(newName)
	m.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	m.dispatchProbeIntentReeval(newName, plan, hasPlan)
}

// RenameSessionAtomic runs doRename under the module's lock and then
// transfers in-memory state from oldName to newName.  This makes the
// entire rename (tmux + DB + in-memory) atomic from the perspective of
// concurrent hook events: any handler that acquires m.mu while a rename
// is in progress observes either the pre-rename or post-rename state,
// never partial state.  If doRename returns an error, the in-memory
// transfer is skipped and the error is propagated.
//
// Tradeoff: doRename is expected to include the tmux rename exec.Command,
// which runs under the lock.  This can delay all concurrent hook handlers
// by the duration of the tmux call (~50ms normally).  This is an intentional
// choice: hook handlers are brief and renames are low-frequency (user-
// triggered), so sacrificing a small amount of throughput during rename
// in exchange for full atomicity is the right tradeoff.  doRename MUST NOT
// call any method that acquires m.mu (would deadlock).
func (m *Module) RenameSessionAtomic(oldName, newName string, doRename func() error) error {
	m.mu.Lock()
	if err := doRename(); err != nil {
		m.mu.Unlock()
		return err
	}
	cancels := m.renameSessionLocked(oldName, newName)
	plan, hasPlan := m.captureProbeIntentReevalLocked(newName)
	m.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	m.dispatchProbeIntentReeval(newName, plan, hasPlan)
	return nil
}

// probeIntentReevalPlan records the (agentType, status) pair captured during
// rename so the post-unlock dispatcher.applyStatus call observes the same
// state the rename observed under m.mu. Routed through a struct (rather
// than two return values) so future fields (e.g. paneID overrides) extend
// without touching every call site.
type probeIntentReevalPlan struct {
	agentType string
	status    agentpkg.Status
}

// captureProbeIntentReevalLocked snapshots the post-rename ProbeIntent
// re-evaluation inputs: the new session's currentStatus + the top frame's
// agentType. Returns hasPlan=false when no currentStatus exists yet (rename
// of a session before any hook fired).
//
// CALLER MUST hold m.mu. lookupTopFrameForSessionLocked + projectionForSession
// touch their own mutexes (frames store, tmux pane resolver) that are
// independent of m.mu, so calling them under m.mu is safe.
func (m *Module) captureProbeIntentReevalLocked(session string) (probeIntentReevalPlan, bool) {
	status, ok := m.currentStatus[session]
	if !ok {
		return probeIntentReevalPlan{}, false
	}
	agentType := ""
	if proj, err := m.projectionForSession(session); err == nil && proj != nil && proj.TopFrame != nil {
		agentType = proj.TopFrame.AgentType
	}
	return probeIntentReevalPlan{agentType: agentType, status: status}, true
}

// dispatchProbeIntentReeval invokes dispatcher.applyStatus outside m.mu so
// the dispatcher can take its own lock. No-op when hasPlan=false (no
// currentStatus to evaluate against) or when dispatcher is nil (defensive;
// New() always wires it).
func (m *Module) dispatchProbeIntentReeval(session string, plan probeIntentReevalPlan, hasPlan bool) {
	if !hasPlan {
		return
	}
	if m.probeIntentDisp == nil {
		return
	}
	m.probeIntentDisp.applyStatus(session, plan.agentType, plan.status)
}

// replayFromDB rebuilds in-memory state from persisted frame projections and
// falls back to legacy agent_events for sessions that have not migrated yet.
func (m *Module) replayFromDB() {
	projectedSessions := make(map[string]struct{})
	if projections, err := m.liveSessionProjections(); err == nil {
		for _, item := range projections {
			projectedSessions[item.SessionName] = struct{}{}
			m.mu.Lock()
			syncProjectionState(m.currentStatus, m.subagents, item.SessionName, &item.Projection)
			m.mu.Unlock()
		}
	} else {
		log.Printf("[agent] replay frames: %v", err)
	}
	all, err := m.events.ListAll()
	if err != nil {
		log.Printf("[agent] replay: %v", err)
		return
	}
	for _, ev := range all {
		if _, ok := projectedSessions[ev.TmuxSession]; ok {
			continue
		}
		provider, ok := m.registry.Get(ev.AgentType)
		if !ok {
			continue
		}
		result := provider.DeriveStatus(ev.EventName, ev.RawEvent)
		if !result.Valid {
			// Pre-W2 stored EventName (e.g. opencode legacy literal "Stop")
			// no longer matches a Pdx-prefixed catalog entry; mirror the
			// hot-path invalid-result cleanup at handler.go:230 so a
			// subsequent sendSnapshot doesn't broadcast a stale row.
			if m.events != nil {
				if err := m.events.Delete(ev.TmuxSession); err != nil {
					log.Printf("[agent] replay cleanup of legacy event: %v", err)
				}
			}
			continue
		}
		if result.Status != "" {
			m.mu.Lock()
			m.currentStatus[ev.TmuxSession] = result.Status
			m.mu.Unlock()
		}
	}
}

// sendSnapshot sends the latest hook event for each known session to a new WS subscriber.
func (m *Module) sendSnapshot(sub *core.EventSubscriber) {
	if m.sessions == nil {
		return
	}
	projectedSessions := make(map[string]struct{})
	if projections, err := m.liveSessionProjections(); err == nil {
		for _, item := range projections {
			projectedSessions[item.SessionName] = struct{}{}
			if item.SessionCode == "" {
				continue
			}
			normalized := buildProjectionNormalized(&item.Projection, item.Projection.TopFrame.AgentType, "replay", time.Now().UnixNano(), agentpkg.DeriveResult{})
			payload, _ := json.Marshal(normalized)
			event := core.HostEvent{Type: "hook", Session: item.SessionCode, Value: string(payload)}
			data, _ := json.Marshal(event)
			sub.Send(data)
			m.mu.Lock()
			syncProjectionState(m.currentStatus, m.subagents, item.SessionName, &item.Projection)
			m.mu.Unlock()
		}
	} else {
		log.Printf("[agent] snapshot frames: %v", err)
	}
	all, err := m.events.ListAll()
	if err != nil {
		log.Printf("[agent] snapshot: %v", err)
		return
	}
	if len(all) == 0 {
		return
	}

	sessions, err := m.sessions.ListSessions()
	if err != nil {
		log.Printf("[agent] snapshot sessions: %v", err)
		return
	}
	nameToCode := make(map[string]string, len(sessions))
	for _, s := range sessions {
		nameToCode[s.Name] = s.Code
	}

	for _, ev := range all {
		if _, ok := projectedSessions[ev.TmuxSession]; ok {
			continue
		}
		code, ok := nameToCode[ev.TmuxSession]
		if !ok {
			continue
		}
		var result agentpkg.DeriveResult
		if provider, ok := m.registry.Get(ev.AgentType); ok {
			result = provider.DeriveStatus(ev.EventName, ev.RawEvent)
		}
		if !result.Valid {
			// Pre-W2 stored EventName no longer matches a Pdx-prefixed
			// catalog entry; mirror the hot-path invalid-result cleanup at
			// handler.go:230 so the SPA doesn't see a resurrected
			// raw_event_name on cold reconnect (which would re-key
			// hook-module lastTrigger and surface stale legacy events
			// despite replayFromDB intentionally skipping them).
			if m.events != nil {
				if err := m.events.Delete(ev.TmuxSession); err != nil {
					log.Printf("[agent] snapshot cleanup of legacy event: %v", err)
				}
			}
			continue
		}
		normalized := m.buildNormalized(ev.TmuxSession, ev.EventName, ev.AgentType, ev.BroadcastTs, result)
		payload, _ := json.Marshal(normalized)
		event := core.HostEvent{Type: "hook", Session: code, Value: string(payload)}
		data, _ := json.Marshal(event)
		sub.Send(data)
	}
}

func (m *Module) liveFrameProjections() ([]SessionProjection, error) {
	if m.frames == nil {
		return nil, nil
	}
	frames, err := m.frames.ListAll()
	if err != nil {
		return nil, err
	}
	if len(frames) == 0 {
		return nil, nil
	}
	frames = m.filterProjectionFrames(frames)
	return BuildSessionProjections(frames), nil
}

func (m *Module) resolvePaneSession(paneID string) (string, string) {
	if m.tmux == nil {
		return "", ""
	}
	sessionName, err := m.tmux.PaneSessionName(paneID)
	if err != nil {
		return "", ""
	}
	return sessionName, m.resolveSessionCode(sessionName)
}

// nextProbeIntentGeneration returns a fresh monotonically increasing
// generation token for ProbeIntent detectors. CALLER MUST hold m.mu (the
// counter is m.mu-protected; call sites are inside applyIntentLifecycle
// which already holds the lock).
//
// Per spec §5.4 — uint64 overflow needs ~5×10¹⁹ increments and is therefore
// not a practical concern for daemon lifetime; no reuse risk.
func (m *Module) nextProbeIntentGeneration() uint64 {
	m.probeIntentGen++
	return m.probeIntentGen
}

// sessionStatusSnapshot pairs a session's top-frame agentType with its
// current status. Used by probeIntentDispatcher.replayStatus to drive
// applyStatus per-session after a daemon-restart hydrate.
type sessionStatusSnapshot struct {
	agentType string
	status    agentpkg.Status
}

// snapshotStatuses returns a session → (agentType, status) snapshot taken
// under m.mu. Used by probeIntentDispatcher.replayStatus (W6-3 P1-T6 /
// issue #698) so a daemon restart re-arms ProbeIntent detectors based on
// post-replay state.
//
// agentType is sourced from the top-frame projection (NOT from the legacy
// agent_events row) so the value matches what live hook handlers would
// produce. Sessions without a resolvable top frame still appear in the
// snapshot with agentType="" — applyIntentLifecycle handles that path
// (frame lookup fails inside m.mu → skip arm).
func (m *Module) snapshotStatuses() map[string]sessionStatusSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]sessionStatusSnapshot, len(m.currentStatus))
	for session, status := range m.currentStatus {
		entry := sessionStatusSnapshot{status: status}
		if proj, err := m.projectionForSession(session); err == nil && proj != nil && proj.TopFrame != nil {
			entry.agentType = proj.TopFrame.AgentType
		}
		out[session] = entry
	}
	return out
}

// lookupTopFrameForSessionLocked returns the top frame's pane_id + pid for
// session. CALLER MUST hold m.mu. Returns ok=false when the session has no
// frame projection or no top frame.
//
// W6-3 P1-T3: used by probeIntentDispatcher to capture the (paneID, senderPID)
// snapshot when arming a ProbeIntent detector. The "Locked" suffix marks the
// caller-locked contract; the implementation reuses projectionForSession,
// whose data sources (m.frames sql store + m.tmux pane resolver) acquire
// their own mutexes that are independent of m.mu, so calling it under m.mu
// does not introduce a lock cycle.
//
// See spec §6.2 — the helper deliberately routes through the existing
// projection pipeline so daemon-restart hydrate replay (P1-T6) and the live
// hook path observe identical (paneID, pid) selection logic.
func (m *Module) lookupTopFrameForSessionLocked(session string) (paneID string, pid int, ok bool) {
	projection, err := m.projectionForSession(session)
	if err != nil || projection == nil || projection.TopFrame == nil {
		return "", 0, false
	}
	return projection.TopFrame.PaneID, projection.TopFrame.PID, true
}

// manageActivityWatch is invoked by hook handlers as a status changes. The
// W3 撤回 stop-only path (no new ScreenChange watcher is ever started here)
// is preserved; W6-3 P1-T5 dispatches the ProbeIntent lifecycle to
// probeIntentDisp.applyStatus afterwards so per-agent probe gating runs on
// every status change.
//
// W3 撤回 rationale: Phase 4a-1 wired this function as the always-on probe
// start site, which violated the "probe is recovery-only" v2.0 contract by
// covering every Waiting/Running/Idle transition with a screen watcher. W6
// reintroduces starts via the ProbeIntent dispatcher (per-agent declared
// intents + lifecycle gating), NOT via probeOrch.startWatch.
//
// R3 fix: m.activeWatchers is owned by this function (and renameSessionLocked);
// the orchestrator deliberately does not touch it.
//
// Locking contract: m.mu is acquired and released INSIDE this function for
// the activeWatchers eviction step; the dispatcher.applyStatus call MUST
// happen AFTER m.mu is released because the dispatcher takes m.mu itself
// (see probe_intent_dispatcher.go reconcileSessionActive +
// applyIntentLifecycle critical sections).
func (m *Module) manageActivityWatch(session, agentType string, newStatus agentpkg.Status) {
	m.mu.Lock()
	_, wasWatching := m.activeWatchers[session]
	delete(m.activeWatchers, session)
	m.mu.Unlock()
	if wasWatching {
		m.probeOrch.stopWatch(session)
	}

	// W6-3 P1-T5: dispatch ProbeIntent lifecycle. dispatcher.applyStatus is
	// safe to call without m.mu held; it routes through reconcileSessionActive
	// + applyIntentLifecycle which take m.mu themselves for active-set
	// mutation, then start / cancel detector goroutines outside the lock.
	if m.probeIntentDisp != nil {
		m.probeIntentDisp.applyStatus(session, agentType, newStatus)
	}
}
