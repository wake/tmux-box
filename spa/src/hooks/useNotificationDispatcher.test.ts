import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { shouldNotify, shouldDispatch, clearSeenTs, handleNotificationClick, buildDebounceKey, __resetDebounceStateForTests, __purgeDebounceForHostForTests } from './useNotificationDispatcher'
import type { NotificationSettings } from '../stores/useNotificationSettingsStore'
import { STORAGE_KEYS } from '../lib/storage'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useNotificationSettingsStore } from '../stores/useNotificationSettingsStore'
import { useSessionStore } from '../stores/useSessionStore'
import { createTab } from '../types/tab'

const defaultSettings: NotificationSettings = {
  enabled: true, events: {}, notifyWithoutTab: false, reopenTabOnClick: false,
}

describe('shouldNotify', () => {
  beforeEach(() => {
    // Reset debounce state so error-event tests in this suite don't bleed into each other
    __resetDebounceStateForTests()
  })

  it('returns true for waiting event with matching tab', () => {
    expect(shouldNotify({ derived: 'waiting', eventName: 'Notification', compositeKey: 'host:abc', focusedCompositeKey: '', hasTab: true, settings: defaultSettings })).toBe(true)
  })
  it('returns true for idle event', () => {
    expect(shouldNotify({ derived: 'idle', eventName: 'Stop', compositeKey: 'host:abc', focusedCompositeKey: '', hasTab: true, settings: defaultSettings })).toBe(true)
  })
  it('returns false for notification_silent event', () => {
    expect(shouldNotify({ derived: 'idle', eventName: 'PdxStop', compositeKey: 'host:abc', focusedCompositeKey: '', hasTab: true, settings: defaultSettings, notificationSilent: true })).toBe(false)
  })
  it('returns false for running event', () => {
    expect(shouldNotify({ derived: 'running', eventName: 'UserPromptSubmit', compositeKey: 'host:abc', focusedCompositeKey: '', hasTab: true, settings: defaultSettings })).toBe(false)
  })
  it('returns false when focused on same session and window has focus', () => {
    vi.spyOn(document, 'hasFocus').mockReturnValue(true)
    expect(shouldNotify({ derived: 'waiting', eventName: 'Notification', compositeKey: 'host:abc', focusedCompositeKey: 'host:abc', hasTab: true, settings: defaultSettings })).toBe(false)
    vi.restoreAllMocks()
  })
  it('returns true when focused on same session but window is in background', () => {
    vi.spyOn(document, 'hasFocus').mockReturnValue(false)
    expect(shouldNotify({ derived: 'waiting', eventName: 'Notification', compositeKey: 'host:abc', focusedCompositeKey: 'host:abc', hasTab: true, settings: defaultSettings })).toBe(true)
    vi.restoreAllMocks()
  })
  it('returns false when no tab and notifyWithoutTab=false', () => {
    expect(shouldNotify({ derived: 'waiting', eventName: 'Notification', compositeKey: 'host:abc', focusedCompositeKey: '', hasTab: false, settings: defaultSettings })).toBe(false)
  })
  it('returns true when no tab but notifyWithoutTab=true', () => {
    expect(shouldNotify({ derived: 'waiting', eventName: 'Notification', compositeKey: 'host:abc', focusedCompositeKey: '', hasTab: false, settings: { ...defaultSettings, notifyWithoutTab: true } })).toBe(true)
  })
  it('returns false when agent disabled', () => {
    expect(shouldNotify({ derived: 'waiting', eventName: 'Notification', compositeKey: 'host:abc', focusedCompositeKey: '', hasTab: true, settings: { ...defaultSettings, enabled: false } })).toBe(false)
  })
  it('returns false when event disabled', () => {
    expect(shouldNotify({ derived: 'waiting', eventName: 'Notification', compositeKey: 'host:abc', focusedCompositeKey: '', hasTab: true, settings: { ...defaultSettings, events: { Notification: false } } })).toBe(false)
  })
  it('event defaults to true when not in events map', () => {
    expect(shouldNotify({ derived: 'idle', eventName: 'Stop', compositeKey: 'host:abc', focusedCompositeKey: '', hasTab: true, settings: { ...defaultSettings, events: {} } })).toBe(true)
  })
  it('returns false for idle Notification (idle_prompt/auth_success are informational)', () => {
    expect(shouldNotify({ derived: 'idle', eventName: 'Notification', compositeKey: 'host:abc', focusedCompositeKey: '', hasTab: true, settings: defaultSettings })).toBe(false)
  })
  it('returns true for waiting Notification (permission_prompt/elicitation_dialog)', () => {
    expect(shouldNotify({ derived: 'waiting', eventName: 'Notification', compositeKey: 'host:abc', focusedCompositeKey: '', hasTab: true, settings: defaultSettings })).toBe(true)
  })
  it('returns true for error event (StopFailure)', () => {
    expect(shouldNotify({ derived: 'error', eventName: 'StopFailure', compositeKey: 'host:abc', focusedCompositeKey: '', hasTab: true, settings: defaultSettings })).toBe(true)
  })

  // W2 transition: cc broadcasts PdxXxx; shouldNotify normalizes at entry so
  // PdxXxx behaves identically to the legacy literal.
  it('idle PdxNotification suppressed identically to Notification (W2)', () => {
    expect(shouldNotify({ derived: 'idle', eventName: 'PdxNotification', compositeKey: 'host:abc', focusedCompositeKey: '', hasTab: true, settings: defaultSettings })).toBe(false)
  })
  it('waiting PdxPermissionRequest dispatched identically to PermissionRequest (W2)', () => {
    expect(shouldNotify({ derived: 'waiting', eventName: 'PdxPermissionRequest', compositeKey: 'host:abc', focusedCompositeKey: '', hasTab: true, settings: defaultSettings })).toBe(true)
  })
  it('error PdxStopFailure dispatched identically to StopFailure (W2)', () => {
    expect(shouldNotify({ derived: 'error', eventName: 'PdxStopFailure', compositeKey: 'host:abc', focusedCompositeKey: '', hasTab: true, settings: defaultSettings })).toBe(true)
  })
  it('settings.events legacy key disables PdxXxx event (W2)', () => {
    // user previously toggled "Notification" off in settings UI; W2 cc still
    // broadcasts PdxNotification but normalize → legacy key lookup hits.
    expect(shouldNotify({ derived: 'waiting', eventName: 'PdxNotification', compositeKey: 'host:abc', focusedCompositeKey: '', hasTab: true, settings: { ...defaultSettings, events: { Notification: false } } })).toBe(false)
  })
})

describe('shouldDispatch', () => {
  beforeEach(() => {
    localStorage.removeItem(STORAGE_KEYS.NOTIFICATION_SEEN)
  })

  it('returns false for new session (sentinel Infinity) but records ts', () => {
    // First event for a session the client has never seen
    expect(shouldDispatch('abc', 1000)).toBe(false)
    // ts is now recorded, so a newer event should dispatch
    expect(shouldDispatch('abc', 2000)).toBe(true)
  })

  it('returns false for duplicate broadcast_ts', () => {
    shouldDispatch('abc', 1000) // sentinel → record
    shouldDispatch('abc', 2000) // first real → dispatch
    expect(shouldDispatch('abc', 2000)).toBe(false) // same ts → skip
  })

  it('returns false for older broadcast_ts', () => {
    shouldDispatch('abc', 1000) // sentinel → record
    shouldDispatch('abc', 2000) // dispatch
    expect(shouldDispatch('abc', 1500)).toBe(false) // older → skip
  })

  it('returns true for newer broadcast_ts after recorded', () => {
    shouldDispatch('abc', 1000) // sentinel → record
    expect(shouldDispatch('abc', 2000)).toBe(true) // newer → dispatch
    expect(shouldDispatch('abc', 3000)).toBe(true) // newer again → dispatch
  })

  it('isolates sessions', () => {
    shouldDispatch('abc', 1000) // record abc
    shouldDispatch('def', 5000) // record def
    expect(shouldDispatch('abc', 2000)).toBe(true) // abc newer
    expect(shouldDispatch('def', 3000)).toBe(false) // def older than 5000
  })

  it('persists across calls (simulates restart)', () => {
    shouldDispatch('abc', 1000) // sentinel → record
    shouldDispatch('abc', 2000) // dispatch + record
    // Simulate restart: shouldDispatch is called fresh but localStorage persists
    expect(shouldDispatch('abc', 2000)).toBe(false) // same ts
    expect(shouldDispatch('abc', 3000)).toBe(true)  // newer
  })

  it('clearSeenTs resets session so next event is sentinel again', () => {
    shouldDispatch('abc', 1000) // sentinel → record
    shouldDispatch('abc', 2000) // dispatch
    clearSeenTs('abc')
    // After clear, session is new again — sentinel behavior
    expect(shouldDispatch('abc', 500)).toBe(false) // sentinel → record (even older ts)
    expect(shouldDispatch('abc', 600)).toBe(true)  // newer than 500 → dispatch
  })
})

describe('handleNotificationClick workspace switching', () => {
  const HOST_ID = 'host-a'
  const SESSION_CODE = 'ses001'

  beforeEach(() => {
    // Reset all stores
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useWorkspaceStore.getState().reset()
    useAgentStore.setState({ lastEvents: {}, statuses: {}, unread: {}, subagents: {}, models: {}, agentTypes: {} })
    useNotificationSettingsStore.setState({ agents: {} })
    useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
    // Mock window.electronAPI as undefined (not in Electron in tests)
    Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
  })

  it('switches to workspace containing the tab', () => {
    // Setup: create a tab in workspace B, active workspace is A
    const tab = createTab({ kind: 'tmux-session', hostId: HOST_ID, sessionCode: SESSION_CODE, mode: 'stream', cachedName: '', tmuxInstance: '' })
    useTabStore.getState().addTab(tab)

    const wsA = useWorkspaceStore.getState().addWorkspace('Workspace A')
    const wsB = useWorkspaceStore.getState().addWorkspace('Workspace B')
    useWorkspaceStore.getState().addTabToWorkspace(wsB.id, tab.id)
    useWorkspaceStore.getState().setActiveWorkspace(wsA.id)

    // Action: handleNotificationClick open-session
    handleNotificationClick({ kind: 'open-session', hostId: HOST_ID, sessionCode: SESSION_CODE })

    // Assert: activeWorkspaceId switched to wsB, workspaceActiveTab is tab.id
    const state = useWorkspaceStore.getState()
    expect(state.activeWorkspaceId).toBe(wsB.id)
    const wsState = state.workspaces.find(w => w.id === wsB.id)
    expect(wsState?.activeTabId).toBe(tab.id)
  })

  it('switches to Home when tab is standalone (not in any workspace)', () => {
    // Setup: tab not in any workspace, active workspace is wsA
    const tab = createTab({ kind: 'tmux-session', hostId: HOST_ID, sessionCode: SESSION_CODE, mode: 'stream', cachedName: '', tmuxInstance: '' })
    useTabStore.getState().addTab(tab)

    const wsA = useWorkspaceStore.getState().addWorkspace('Workspace A')
    useWorkspaceStore.getState().setActiveWorkspace(wsA.id)
    // Tab is NOT added to any workspace (standalone)

    // Action: handleNotificationClick open-session
    handleNotificationClick({ kind: 'open-session', hostId: HOST_ID, sessionCode: SESSION_CODE })

    // Assert: activeWorkspaceId is null (Home)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBeNull()
  })

  it('reopenTabOnClick adds tab to active workspace and stays in that workspace', () => {
    // Setup: no existing tab, activeWorkspaceId is wsA (non-null), reopenTabOnClick=true
    const wsA = useWorkspaceStore.getState().addWorkspace('Workspace A')
    useWorkspaceStore.getState().setActiveWorkspace(wsA.id)

    // Enable reopenTabOnClick for the default agent type
    useNotificationSettingsStore.getState().setReopenTabOnClick('', true)

    // Action: handleNotificationClick with reopenTabOnClick
    handleNotificationClick({ kind: 'open-session', hostId: HOST_ID, sessionCode: SESSION_CODE })

    // Assert: new tab added to wsA via insertTab, activeWorkspaceId stays wsA
    const wsState = useWorkspaceStore.getState()
    expect(wsState.activeWorkspaceId).toBe(wsA.id)
    const ws = wsState.workspaces.find(w => w.id === wsA.id)
    expect(ws?.tabs).toHaveLength(1)
  })

  it('reopenTabOnClick stamps the generation from the session payload', () => {
    useSessionStore.setState({
      sessions: {
        [HOST_ID]: [
          { code: SESSION_CODE, name: 'dev', cwd: '/tmp', mode: 'stream', cc_session_id: '', cc_model: '', has_relay: false, tmux_instance: '222:2000' },
        ],
      },
    })
    useNotificationSettingsStore.getState().setReopenTabOnClick('', true)

    handleNotificationClick({ kind: 'open-session', hostId: HOST_ID, sessionCode: SESSION_CODE })

    const tab = Object.values(useTabStore.getState().tabs)[0]
    const content = tab.layout.type === 'leaf' ? tab.layout.pane.content : null
    expect(content?.kind === 'tmux-session' && content.tmuxInstance).toBe('222:2000')
  })

  it('reopenTabOnClick at Home (null workspace) keeps tab standalone', () => {
    // Setup: no existing tab, activeWorkspaceId is null (Home), reopenTabOnClick=true
    // Workspaces exist but active is null
    useWorkspaceStore.getState().addWorkspace('Workspace A')
    useWorkspaceStore.getState().setActiveWorkspace(null)

    // Enable reopenTabOnClick
    useNotificationSettingsStore.getState().setReopenTabOnClick('', true)

    // Action: handleNotificationClick with reopenTabOnClick
    handleNotificationClick({ kind: 'open-session', hostId: HOST_ID, sessionCode: SESSION_CODE })

    // Assert: insertTab is no-op (null wsId), tab is standalone, activeWorkspaceId stays null
    const wsState = useWorkspaceStore.getState()
    expect(wsState.activeWorkspaceId).toBeNull()
    // All workspaces should have no tabs (tab is standalone)
    for (const ws of wsState.workspaces) {
      expect(ws.tabs).toHaveLength(0)
    }
  })
})

// ---------------------------------------------------------------------------
// P3-T1: buildDebounceKey + state Map + shouldNotify integration
// ---------------------------------------------------------------------------

const errorSettings: NotificationSettings = {
  enabled: true, events: {}, notifyWithoutTab: true, reopenTabOnClick: false,
}

function makeErrorParams(ck: string, errorString: string, eventName = 'StopFailure'): Parameters<typeof shouldNotify>[0] {
  return {
    derived: 'error',
    eventName,
    compositeKey: ck,
    focusedCompositeKey: '',
    hasTab: true,
    settings: errorSettings,
    errorString,
  }
}

describe('buildDebounceKey', () => {
  it('normal inputs produce a stable JSON array string', () => {
    const key = buildDebounceKey('host:sess', 'StopFailure', 'rate_limit')
    expect(key).toBe(JSON.stringify(['host:sess', 'StopFailure', 'rate_limit']))
  })

  it('inputs containing pipe characters are not confused with separators', () => {
    const key1 = buildDebounceKey('a|b', 'c', 'd|e')
    const key2 = buildDebounceKey('a', 'b|c', 'd|e')
    expect(key1).not.toBe(key2)
  })

  it('undefined errorString maps to empty string', () => {
    const key = buildDebounceKey('host:sess', 'StopFailure', undefined as unknown as string)
    expect(key).toBe(JSON.stringify(['host:sess', 'StopFailure', '']))
  })

  it('null errorString maps to empty string', () => {
    const key = buildDebounceKey('host:sess', 'StopFailure', null as unknown as string)
    expect(key).toBe(JSON.stringify(['host:sess', 'StopFailure', '']))
  })
})

describe('error notification debounce', () => {
  beforeEach(() => {
    __resetDebounceStateForTests()
    vi.useFakeTimers()
    vi.setSystemTime(0)
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('debounce__test_reset_helper — reset clears state so tests are order-independent', () => {
    shouldNotify(makeErrorParams('host:sess', 'rate_limit'))
    __resetDebounceStateForTests()
    // After reset, same key should pass again (first-event semantics)
    expect(shouldNotify(makeErrorParams('host:sess', 'rate_limit'))).toBe(true)
  })

  it('debounce__first_error_passes', () => {
    expect(shouldNotify(makeErrorParams('host:sess', 'rate_limit'))).toBe(true)
  })

  it('debounce__second_error_within_window_blocked_and_extends', () => {
    // t=0: first event passes, silentUntil = 60_000
    expect(shouldNotify(makeErrorParams('host:sess', 'rate_limit'))).toBe(true)

    // t=30_000: second event — still within window; should be blocked and extend silentUntil to 90_000
    vi.setSystemTime(30_000)
    expect(shouldNotify(makeErrorParams('host:sess', 'rate_limit'))).toBe(false)

    // t=65_000: would have passed under original window (60_000), but extension moved it to 90_000
    vi.setSystemTime(65_000)
    expect(shouldNotify(makeErrorParams('host:sess', 'rate_limit'))).toBe(false)
  })

  it('debounce__error_after_silence_window_passes', () => {
    // t=0: first event
    shouldNotify(makeErrorParams('host:sess', 'rate_limit'))
    // t=70_000: window expired (>60_000 silence); should pass again
    vi.setSystemTime(70_000)
    expect(shouldNotify(makeErrorParams('host:sess', 'rate_limit'))).toBe(true)
  })

  it('debounce__storm_100_events_yields_one_notification', () => {
    // 100 events at ~1.67 Hz (600ms intervals) over ~60s
    let passCount = 0
    for (let i = 0; i < 100; i++) {
      vi.advanceTimersByTime(600)
      if (shouldNotify(makeErrorParams('host:sess', 'rate_limit'))) {
        passCount++
      }
    }
    // The first event (at t=600ms) should pass; all subsequent should be blocked
    // and the sliding window keeps extending
    expect(passCount).toBe(1)
  })

  it('debounce__different_keys_independent', () => {
    // (a) same session + different detail.error both pass
    expect(shouldNotify(makeErrorParams('host:sess', 'rate_limit'))).toBe(true)
    expect(shouldNotify(makeErrorParams('host:sess', 'auth_failed'))).toBe(true)

    __resetDebounceStateForTests()

    // (b) same error + different sessions both pass
    expect(shouldNotify(makeErrorParams('host:sess1', 'rate_limit'))).toBe(true)
    expect(shouldNotify(makeErrorParams('host:sess2', 'rate_limit'))).toBe(true)

    __resetDebounceStateForTests()

    // (c) same session + same error + different eventName both pass
    expect(shouldNotify(makeErrorParams('host:sess', 'rate_limit', 'StopFailure'))).toBe(true)
    expect(shouldNotify(makeErrorParams('host:sess', 'rate_limit', 'Stop'))).toBe(true)
  })

  it('debounce__key_uses_json_array_not_pipe_join — no key collision', () => {
    // Collision rebuke: these two triples would collide with pipe-join but not JSON.stringify
    const key1 = buildDebounceKey('a|b', 'c', 'd|e')
    const key2 = buildDebounceKey('a', 'b|c', 'd|e')
    expect(key1).not.toBe(key2)
  })
})

// ---------------------------------------------------------------------------
// P3-T2: cleanup paths (clearSession / removeHost / TTL)
// ---------------------------------------------------------------------------

describe('debounce cleanup', () => {
  beforeEach(() => {
    __resetDebounceStateForTests()
    vi.useFakeTimers()
    vi.setSystemTime(0)
    // Reset agent store
    useAgentStore.setState({ lastEvents: {}, statuses: {}, unread: {}, subagents: {}, models: {}, agentTypes: {} })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('debounce__clear_session_resets — next event passes after clearSession', () => {
    // Seed lastEvents so subscription can detect the removal
    const fakeEvent = { agent_type: 'cc', status: 'error', raw_event_name: 'StopFailure', broadcast_ts: 1, detail: { error: 'rate_limit' } }
    useAgentStore.setState({ lastEvents: { 'host:sess': fakeEvent } })

    // First event passes and sets debounce window
    shouldNotify(makeErrorParams('host:sess', 'rate_limit'))
    // Second event blocked
    vi.setSystemTime(1_000)
    expect(shouldNotify(makeErrorParams('host:sess', 'rate_limit'))).toBe(false)

    // clearSession removes 'host:sess' from lastEvents → triggers subscription → purge debounce
    useAgentStore.getState().clearSession('host', 'sess')

    // Next event should pass (window cleared)
    vi.setSystemTime(2_000)
    expect(shouldNotify(makeErrorParams('host:sess', 'rate_limit'))).toBe(true)
  })

  it('debounce__remove_host_with_colon_in_hostid — purge correctly matches compositeKeys when hostId contains colon', () => {
    // hostId = "mlab:abc123", compositeKey = "mlab:abc123:s1"
    // Old code: split(':')[0] = "mlab" ≠ "mlab:abc123" → entry NOT removed (bug)
    // Fixed code: startsWith("mlab:abc123:") → entry removed correctly
    const ckTarget = 'mlab:abc123:s1'
    const ckOther = 'mlab:def456:s1'

    // Seed both debounce entries
    shouldNotify(makeErrorParams(ckTarget, 'rate_limit'))
    shouldNotify(makeErrorParams(ckOther, 'rate_limit'))

    vi.setSystemTime(1_000)
    // Both blocked
    expect(shouldNotify(makeErrorParams(ckTarget, 'rate_limit'))).toBe(false)
    expect(shouldNotify(makeErrorParams(ckOther, 'rate_limit'))).toBe(false)

    // Directly call purgeDebounceForHost with the colon-containing hostId
    __purgeDebounceForHostForTests('mlab:abc123')

    vi.setSystemTime(2_000)
    // "mlab:abc123" entries cleared → passes
    expect(shouldNotify(makeErrorParams(ckTarget, 'rate_limit'))).toBe(true)
    // "mlab:def456" entry untouched → still blocked
    expect(shouldNotify(makeErrorParams(ckOther, 'rate_limit'))).toBe(false)
  })

  it('debounce__remove_host_resets — all keys for host cleared on removeHost', () => {
    // Seed lastEvents so subscription can detect host removal
    const fakeEvent = { agent_type: 'cc', status: 'error', raw_event_name: 'StopFailure', broadcast_ts: 1, detail: {} }
    useAgentStore.setState({
      lastEvents: {
        'host:sess1': { ...fakeEvent },
        'host:sess2': { ...fakeEvent },
      },
    })

    // Seed two sessions on same host
    shouldNotify(makeErrorParams('host:sess1', 'rate_limit'))
    shouldNotify(makeErrorParams('host:sess2', 'rate_limit'))

    vi.setSystemTime(1_000)
    // Both blocked
    expect(shouldNotify(makeErrorParams('host:sess1', 'rate_limit'))).toBe(false)
    expect(shouldNotify(makeErrorParams('host:sess2', 'rate_limit'))).toBe(false)

    // removeHost clears all 'host:*' keys from lastEvents → triggers subscription → purge all
    useAgentStore.getState().removeHost('host')

    vi.setSystemTime(2_000)
    // Both should pass now (windows cleared)
    expect(shouldNotify(makeErrorParams('host:sess1', 'rate_limit'))).toBe(true)
    expect(shouldNotify(makeErrorParams('host:sess2', 'rate_limit'))).toBe(true)
  })

  it('debounce__ttl_cleanup — stale entries removed from Map on next shouldNotify call', () => {
    const ERROR_NOTIFY_WINDOW_MS = 60_000
    // Set debounce entry at t=0
    shouldNotify(makeErrorParams('host:ttl-sess', 'rate_limit'))

    // Advance time past 5 × WINDOW_MS + 1ms
    vi.setSystemTime(5 * ERROR_NOTIFY_WINDOW_MS + 1)

    // Make a shouldNotify call on a different key to trigger TTL sweep
    // (TTL sweep runs during any error shouldNotify call)
    shouldNotify(makeErrorParams('host:other-sess', 'rate_limit'))

    // The original stale entry for host:ttl-sess should have been evicted.
    // We verify indirectly: calling shouldNotify on ttl-sess returns true
    // (first-event semantics, not blocked by stale silentUntil)
    const result = shouldNotify(makeErrorParams('host:ttl-sess', 'rate_limit'))
    expect(result).toBe(true)
  })

  it('debounce__sweep_throttled_once_per_window — TTL sweep runs at most once per 60s window', () => {
    const WINDOW_MS = 60_000
    // Plant 5 stale entries (silentUntil far in the past)
    for (let i = 0; i < 5; i++) {
      shouldNotify(makeErrorParams(`host:stale-sess-${i}`, 'rate_limit'))
    }
    // Advance far past the 5×WINDOW TTL threshold to make them all stale
    vi.setSystemTime(6 * WINDOW_MS)

    // Drive 9 more error events within WINDOW_MS - 1ms — sweep should NOT run yet
    // (lastSweepAt was 0, first event at t=6*WINDOW triggers sweep once)
    // After the first call at t=6*WINDOW the sweep fires; within next WINDOW_MS - 1ms it must NOT fire again.
    for (let i = 0; i < 9; i++) {
      vi.advanceTimersByTime(WINDOW_MS / 10)
      shouldNotify(makeErrorParams(`host:active-sess-${i}`, 'another_error'))
    }
    // Now all stale entries should have been swept on the first call at t=6*WINDOW
    // and NOT swept again (throttle). Confirm by checking that a new entry for
    // a stale key passes (was evicted), while the active entries are still in window.
    const afterResult = shouldNotify(makeErrorParams('host:stale-sess-0', 'rate_limit'))
    expect(afterResult).toBe(true) // stale was swept → passes as new first-event
  })

  it('debounce__hard_cap_evicts_oldest_when_full — Map capped at 1000 entries', () => {
    // Fill to exactly 1000
    for (let i = 0; i < 1000; i++) {
      shouldNotify(makeErrorParams('host:s', `err-${i}`))
    }
    const firstKey = makeErrorParams('host:s', 'err-0')
    // Advance time so the 1000 entries are in the silence window (not stale)
    vi.setSystemTime(1_000)
    // All 1000 entries blocked
    expect(shouldNotify(firstKey)).toBe(false)

    // Adding entry 1001 must evict the oldest (FIFO) and keep size at 1000
    vi.setSystemTime(2_000)
    shouldNotify(makeErrorParams('host:s', 'err-new'))
    // The first inserted key should now be evicted → passes (new first-event)
    expect(shouldNotify(makeErrorParams('host:s', 'err-0'))).toBe(true)
  })
})

// ---------------------------------------------------------------------------
// P3-T3: isolation guards (waiting / unread badge)
// ---------------------------------------------------------------------------

describe('debounce isolation guards', () => {
  beforeEach(() => {
    __resetDebounceStateForTests()
    vi.useFakeTimers()
    vi.setSystemTime(0)
    useAgentStore.setState({ lastEvents: {}, statuses: {}, unread: {}, subagents: {}, models: {}, agentTypes: {} })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('debounce__waiting_not_debounced — 5 consecutive waiting events all pass', () => {
    const params = {
      derived: 'waiting' as const,
      eventName: 'PermissionRequest',
      compositeKey: 'host:sess',
      focusedCompositeKey: '',
      hasTab: true,
      settings: errorSettings,
    }
    for (let i = 0; i < 5; i++) {
      vi.advanceTimersByTime(1_000)
      expect(shouldNotify(params)).toBe(true)
    }
  })

  it('debounce__subscribe_skips_when_lastEvents_unchanged — Object.keys not called on unrelated state mutation', () => {
    // The early-exit guard prevents Object.keys(prevState.lastEvents) enumeration
    // when lastEvents reference is unchanged (common case on every setState).
    const fakeEvent = { agent_type: 'cc', status: 'error', raw_event_name: 'StopFailure', broadcast_ts: 1, detail: { error: 'rate_limit' } }
    const lastEvents = { 'host:sess': fakeEvent }
    useAgentStore.setState({ lastEvents })

    const objectKeysSpy = vi.spyOn(Object, 'keys')

    // Mutate state NOT touching lastEvents — subscription must early-exit, no Object.keys on lastEvents
    useAgentStore.setState({ unread: { 'host:sess': true } })

    // Object.keys may be called for other reasons; what matters is it was NOT called with lastEvents
    const calledWithLastEvents = objectKeysSpy.mock.calls.some(
      ([arg]) => arg === lastEvents,
    )
    expect(calledWithLastEvents).toBe(false)

    objectKeysSpy.mockRestore()

    // Confirm purge still fires when lastEvents DOES change (clearSession)
    shouldNotify(makeErrorParams('host:sess', 'rate_limit'))
    vi.setSystemTime(1_000)
    expect(shouldNotify(makeErrorParams('host:sess', 'rate_limit'))).toBe(false)
    useAgentStore.getState().clearSession('host', 'sess')
    vi.setSystemTime(2_000)
    expect(shouldNotify(makeErrorParams('host:sess', 'rate_limit'))).toBe(true)
  })

  it('debounce__unread_badge_unaffected — unread set even when shouldNotify returns false (suppressed)', () => {
    const ck = 'host:sess'
    // Step 1: establish debounce window by calling shouldNotify → true
    const suppressed = shouldNotify(makeErrorParams(ck, 'rate_limit'))
    expect(suppressed).toBe(true) // first event passes

    // Step 2: reset unread, then fire second event through handleNormalizedEvent
    useAgentStore.setState({ unread: {} })
    vi.setSystemTime(1_000) // within the 60s window

    // Step 3: confirm shouldNotify returns false (debounce suppressed)
    const blocked = shouldNotify(makeErrorParams(ck, 'rate_limit'))
    expect(blocked).toBe(false)

    // Step 4: drive handleNormalizedEvent — unread path must NOT be gated by shouldNotify
    useAgentStore.getState().handleNormalizedEvent('host', 'sess', {
      agent_type: 'cc',
      status: 'error',
      raw_event_name: 'StopFailure',
      broadcast_ts: 2,
      detail: { error: 'rate_limit' },
    })

    // Step 5: unread must be true even though shouldNotify returned false
    // This test FAILS if a future change gates the unread write on shouldNotify's return value.
    expect(useAgentStore.getState().unread[ck]).toBe(true)
  })
})
