import { useEffect } from 'react'
import { useAgentStore } from '../stores/useAgentStore'
import { getActiveSessionInfo } from '../lib/active-session'
import { compositeKey } from '../lib/composite-key'
import { useI18nStore } from '../stores/useI18nStore'
import { useNotificationSettingsStore } from '../stores/useNotificationSettingsStore'
import type { NotificationSettings } from '../stores/useNotificationSettingsStore'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useSessionStore } from '../stores/useSessionStore'
import { buildNotificationContent } from '../lib/notification-content'
import { normalizeEventName } from '../lib/event-name'
import { findTabBySessionCode, getPrimaryPane } from '../lib/pane-tree'
import { getPlatformCapabilities } from '../lib/platform'
import { useHostStore } from '../stores/useHostStore'
import { createTab } from '../types/tab'
import { STORAGE_KEYS } from '../lib/storage'

// ---------------------------------------------------------------------------
// Error notification debounce (spec §4)
// ---------------------------------------------------------------------------

const ERROR_NOTIFY_WINDOW_MS = 60_000

/** Module-level debounce state. Not in Zustand — dispatcher-private ephemeral state. */
const errorDebounceState = new Map<string, { silentUntil: number }>()

// Max debounce entries: prevents unbounded growth with high-cardinality errorStrings.
// Oldest entry (insertion order) is evicted when the cap is reached.
const MAX_DEBOUNCE_ENTRIES = 1000

// Throttle TTL sweep to at most once per ERROR_NOTIFY_WINDOW_MS to avoid O(N) on every event.
let lastSweepAt = 0

/** Build a collision-free debounce key from the 3-tuple that identifies an error bucket. */
export function buildDebounceKey(ck: string, eventName: string, errorString: string): string {
  // Why: JSON.stringify over a fixed-length array escapes separator chars,
  // preventing collisions that a pipe-join scheme would produce.
  return JSON.stringify([ck, eventName, String(errorString ?? '')])
}

/** Testing-only seam: clear module-level Map and sweep timer between tests. */
export function __resetDebounceStateForTests(): void {
  errorDebounceState.clear()
  lastSweepAt = 0
}

/** Iterate debounce Map, parsing each key once and calling cb(parsed, rawKey). */
function forEachDebounceKey(cb: (parsed: [string, string, string], rawKey: string) => void): void {
  for (const rawKey of errorDebounceState.keys()) {
    try {
      const parsed = JSON.parse(rawKey) as [string, string, string]
      cb(parsed, rawKey)
    } catch {
      // Defensive: skip malformed keys (should never happen under normal use)
    }
  }
}

/** Remove all debounce entries for a given compositeKey (hostId:sessionCode). */
function purgeDebounceForCompositeKey(ck: string): void {
  const toDelete: string[] = []
  forEachDebounceKey((parsed, rawKey) => {
    if (parsed[0] === ck) toDelete.push(rawKey)
  })
  for (const k of toDelete) errorDebounceState.delete(k)
}

/** Remove all debounce entries for all sessions under a given hostId. */
function purgeDebounceForHost(hostId: string): void {
  const toDelete: string[] = []
  // Why: use startsWith(hostId + ':') instead of split(':')[0] so hostIds
  // that themselves contain ':' (e.g. "mlab:abc123") match correctly.
  forEachDebounceKey((parsed, rawKey) => {
    if (parsed[0].startsWith(hostId + ':')) toDelete.push(rawKey)
  })
  for (const k of toDelete) errorDebounceState.delete(k)
}

/** Testing-only seam: expose purgeDebounceForHost for unit tests. */
export function __purgeDebounceForHostForTests(hostId: string): void {
  purgeDebounceForHost(hostId)
}

// Subscribe to store changes to clean up debounce state when sessions/hosts are removed.
// Module-level subscription: registered once at import time, no teardown needed for this pattern.
useAgentStore.subscribe((state, prevState) => {
  // Fast-path: skip enumeration entirely when lastEvents reference is unchanged.
  // Why: handleNormalizedEvent triggers multiple setState calls per event; most don't touch lastEvents.
  if (state.lastEvents === prevState.lastEvents) return

  // Detect removed sessions (lastEvents key disappeared)
  for (const key of Object.keys(prevState.lastEvents)) {
    if (!(key in state.lastEvents)) {
      purgeDebounceForCompositeKey(key)
    }
  }
  // Detect removed hosts (any key whose host prefix is gone)
  const prevHostIds = new Set(Object.keys(prevState.lastEvents).map(k => k.split(':')[0]))
  const currHostIds = new Set(Object.keys(state.lastEvents).map(k => k.split(':')[0]))
  for (const hostId of prevHostIds) {
    if (!currHostIds.has(hostId)) {
      purgeDebounceForHost(hostId)
    }
  }
})

export type NotificationAction =
  | { kind: 'open-session'; hostId: string; sessionCode: string }
  | { kind: 'open-host'; hostId: string }

/** Check if a notification should be dispatched based on broadcast_ts dedup.
 *  New sessions default to Infinity (sentinel), so their first event is recorded
 *  but not dispatched — prevents snapshot flooding on new/restarted clients. */
export function shouldDispatch(sessionCode: string, broadcastTs: number): boolean {
  const data: Record<string, number> = JSON.parse(localStorage.getItem(STORAGE_KEYS.NOTIFICATION_SEEN) || '{}')
  const stored = data[sessionCode] ?? Infinity

  if (broadcastTs <= stored) {
    if (stored === Infinity) {
      data[sessionCode] = broadcastTs
      localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN, JSON.stringify(data))
    }
    return false
  }

  data[sessionCode] = broadcastTs
  localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN, JSON.stringify(data))
  return true
}

/** Remove a session's lastSeenTs entry (called on SessionEnd to prevent
 *  stale timestamps from blocking notifications if the code is reused). */
export function clearSeenTs(sessionCode: string): void {
  const data: Record<string, number> = JSON.parse(localStorage.getItem(STORAGE_KEYS.NOTIFICATION_SEEN) || '{}')
  delete data[sessionCode]
  localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN, JSON.stringify(data))
}

interface ShouldNotifyParams {
  derived: string | null
  eventName: string
  compositeKey: string
  focusedCompositeKey: string
  hasTab: boolean
  settings: NotificationSettings
  notificationSilent?: boolean
  /** Caller extracts from event.detail?.error before passing in (spec §4, option A). */
  errorString?: string
}

export function shouldNotify(params: ShouldNotifyParams): boolean {
  const { derived, eventName: rawEventName, compositeKey: ck, focusedCompositeKey, hasTab, settings, notificationSilent = false, errorString } = params
  // W2 transition: cc broadcasts PdxXxx; legacy literal keys live in shouldNotify
  // suppression checks and NotificationSettings.events. Normalize once at entry.
  const eventName = normalizeEventName(rawEventName)
  if (derived !== 'waiting' && derived !== 'idle' && derived !== 'error') return false
  if (notificationSilent) return false
  // Informational Notification subtypes (idle_prompt, auth_success) derive to 'idle'
  // but should not trigger desktop notifications — consistent with unread marking logic.
  if (derived === 'idle' && eventName === 'Notification') return false
  if (!settings.enabled) return false
  if (settings.events[eventName] === false) return false
  if (!hasTab && !settings.notifyWithoutTab) return false
  // Only suppress when user is actively looking at this session:
  // both the app window must be focused AND the session tab must be active.
  if (focusedCompositeKey === ck && document.hasFocus()) return false

  // Trailing-edge sliding debounce for error notifications (spec §4.1–§4.3).
  // Gate is only for derived==='error'; waiting/idle are not affected.
  if (derived === 'error') {
    const key = buildDebounceKey(ck, eventName, errorString ?? '')
    const now = Date.now()

    // TTL self-cleanup: throttled sweep — at most once per ERROR_NOTIFY_WINDOW_MS.
    // Why: avoids O(N) scan on every high-frequency error event.
    if (now - lastSweepAt >= ERROR_NOTIFY_WINDOW_MS) {
      for (const [k, v] of errorDebounceState) {
        if (v.silentUntil < now - 5 * ERROR_NOTIFY_WINDOW_MS) errorDebounceState.delete(k)
      }
      lastSweepAt = now
    }

    const entry = errorDebounceState.get(key)
    if (entry && now < entry.silentUntil) {
      // Within silence window: extend (trailing-edge sliding) and suppress.
      entry.silentUntil = now + ERROR_NOTIFY_WINDOW_MS
      return false
    }
    // First event or window expired: enforce hard cap before inserting new entry.
    if (entry == null && errorDebounceState.size >= MAX_DEBOUNCE_ENTRIES) {
      // Evict oldest entry (Map preserves insertion order).
      errorDebounceState.delete(errorDebounceState.keys().next().value as string)
    }
    errorDebounceState.set(key, { silentUntil: now + ERROR_NOTIFY_WINDOW_MS })
  }

  return true
}

export function useNotificationDispatcher(): void {
  useEffect(() => {
    const unsubscribe = useAgentStore.subscribe((state, prevState) => {
      const prevEvents = prevState.lastEvents
      const currentEvents = state.lastEvents

      // Clean up lastSeenTs for sessions that ended (prevents stale ts
      // blocking notifications if the session code is reused).
      for (const key of Object.keys(prevEvents)) {
        if (!currentEvents[key]) {
          clearSeenTs(key)
        }
      }

      for (const [compositeKeyStr, event] of Object.entries(currentEvents)) {
        const prev = prevEvents[compositeKeyStr]
        if (prev && prev.broadcast_ts === event.broadcast_ts) continue

        // Extract sessionCode from composite key (hostId:sessionCode)
        const colonIdx = compositeKeyStr.indexOf(':')
        const hostId = colonIdx >= 0 ? compositeKeyStr.slice(0, colonIdx) : ''
        const sessionCode = colonIdx >= 0 ? compositeKeyStr.slice(colonIdx + 1) : compositeKeyStr

        // Dedup layer 1: localStorage-based persistent dedup (handles restart/snapshot).
        // New sessions use Infinity sentinel — first event is recorded but not dispatched.
        // Layer 2: shouldNotify checks active session (derived from activeTabId) + document.hasFocus().
        // Layer 3: Electron main process recentBroadcasts dedup (5s window, multi-window).
        if (!shouldDispatch(compositeKeyStr, event.broadcast_ts)) continue

        const derived = event.status || null
        const tabs = useTabStore.getState().tabs
        const hasTab = findTabBySessionCode(tabs, sessionCode) !== undefined
        const settings = useNotificationSettingsStore.getState().getSettingsForAgent(event.agent_type || '')
        const activeInfo = getActiveSessionInfo()
        const focusedCompositeKey = activeInfo ? compositeKey(activeInfo.hostId, activeInfo.sessionCode) : ''

        // Extract errorString before passing to shouldNotify (spec §4, option A).
        const errorString = String(event.detail?.error ?? '')

        if (!shouldNotify({
          derived,
          eventName: event.raw_event_name,
          compositeKey: compositeKeyStr,
          focusedCompositeKey,
          hasTab,
          settings,
          notificationSilent: event.detail?.notification_silent === true,
          errorString,
        })) continue

        const sessionsMap = useSessionStore.getState().sessions
        const hostSessions = sessionsMap[hostId] ?? []
        const session = hostSessions.find((s) => s.code === sessionCode)
        const sessionName = session?.name || sessionCode

        const content = buildNotificationContent(event.raw_event_name, (event.detail ?? {}) as Record<string, unknown>, sessionName, useI18nStore.getState().t)
        if (!content) continue

        const capabilities = getPlatformCapabilities()
        if (capabilities.canNotification && window.electronAPI?.showNotification) {
          window.electronAPI.showNotification({
            title: content.title,
            body: content.body,
            sessionCode,
            eventName: event.raw_event_name,
            broadcastTs: event.broadcast_ts,
            action: { kind: 'open-session', hostId, sessionCode },
          })
        } else if ('Notification' in window && Notification.permission === 'granted') {
          const n = new Notification(content.title, { body: content.body })
          n.onclick = () => handleNotificationClick({ kind: 'open-session', hostId, sessionCode })
        }
      }
    })
    return unsubscribe
  }, [])

  // Electron notification click listener
  useEffect(() => {
    if (!window.electronAPI?.onNotificationClicked) return
    return window.electronAPI.onNotificationClicked((payload) => {
      // If the payload carries an explicit action, use it directly
      if (payload.action) {
        if (payload.action.kind === 'open-host') {
          handleNotificationClick({ kind: 'open-host', hostId: payload.action.hostId })
        } else {
          handleNotificationClick({
            kind: 'open-session',
            hostId: payload.action.hostId,
            sessionCode: payload.action.sessionCode ?? payload.sessionCode,
          })
        }
        return
      }
      // Backwards compat: no action field — fall back to open-session
      const tabs = useTabStore.getState().tabs
      const tabId = findTabBySessionCode(tabs, payload.sessionCode)
      let hostId = useHostStore.getState().hostOrder[0] ?? ''
      if (tabId) {
        const tab = tabs[tabId]
        const primary = getPrimaryPane(tab.layout)
        if (primary.content.kind === 'tmux-session') hostId = primary.content.hostId
      }
      handleNotificationClick({ kind: 'open-session', hostId, sessionCode: payload.sessionCode })
    })
  }, [])

  // L2/L3 connection notifications (daemon refused, tmux down)
  useEffect(() => {
    const prevState: Record<string, { daemon?: string; tmux?: string }> = {}

    const unsubscribe = useHostStore.subscribe((state) => {
      const t = useI18nStore.getState().t

      for (const hostId of state.hostOrder) {
        const rt = state.runtime[hostId]
        const prev = prevState[hostId]
        const hostName = state.hosts[hostId]?.name ?? hostId

        // L2: daemon refused (was connected, now refused)
        if (prev?.daemon === 'connected' && rt?.daemonState === 'refused') {
          sendConnectionNotification(
            t('notification.daemon_refused', { name: hostName }),
            { kind: 'open-host', hostId },
          )
        }
        // L3: tmux down (was ok, now unavailable)
        if (prev?.tmux === 'ok' && rt?.tmuxState === 'unavailable') {
          sendConnectionNotification(
            t('notification.tmux_down', { name: hostName }),
            { kind: 'open-host', hostId },
          )
        }

        prevState[hostId] = { daemon: rt?.daemonState, tmux: rt?.tmuxState }
      }
    })
    return unsubscribe
  }, [])
}

export function handleNotificationClick(action: NotificationAction): void {
  switch (action.kind) {
    case 'open-session': {
      const { hostId, sessionCode } = action
      const tabs = useTabStore.getState().tabs
      const tabId = findTabBySessionCode(tabs, sessionCode)
      const ck = `${hostId}:${sessionCode}`
      const event = useAgentStore.getState().lastEvents[ck]
      const agentSettings = useNotificationSettingsStore.getState().getSettingsForAgent(event?.agent_type || '')

      let handled = false
      if (tabId) {
        useTabStore.getState().setActiveTab(tabId)
        const ws = useWorkspaceStore.getState().findWorkspaceByTab(tabId)
        if (ws) {
          useWorkspaceStore.getState().setActiveWorkspace(ws.id)
          useWorkspaceStore.getState().setWorkspaceActiveTab(ws.id, tabId)
        } else {
          useWorkspaceStore.getState().setActiveWorkspace(null)
        }
        handled = true
      } else if (agentSettings.reopenTabOnClick) {
        const session = useSessionStore.getState().sessions[hostId]?.find(s => s.code === sessionCode)
        const sessionName = session?.name ?? ''
        // Generation from the session payload we are reopening (spec §4.5);
        // '' when the session is not in the cache = unknown, never a match.
        const newTab = createTab({ kind: 'tmux-session', hostId, sessionCode, mode: 'stream', cachedName: sessionName, tmuxInstance: session?.tmux_instance ?? '' })
        useTabStore.getState().addTab(newTab)
        useTabStore.getState().setActiveTab(newTab.id)
        useWorkspaceStore.getState().insertTab(newTab.id)
        const ws = useWorkspaceStore.getState().findWorkspaceByTab(newTab.id)
        if (ws) {
          useWorkspaceStore.getState().setActiveWorkspace(ws.id)
        } else {
          useWorkspaceStore.getState().setActiveWorkspace(null)
        }
        handled = true
      }

      if (handled) {
        useAgentStore.getState().markRead(hostId, sessionCode)
      }
      if (handled && window.electronAPI?.focusMyWindow) {
        window.electronAPI.focusMyWindow()
      }
      break
    }
    case 'open-host': {
      useTabStore.getState().openSingletonTab({ kind: 'hosts' })
      useHostStore.getState().setActiveHost(action.hostId)
      if (window.electronAPI?.focusMyWindow) {
        window.electronAPI.focusMyWindow()
      }
      break
    }
  }
}

function sendConnectionNotification(message: string, action: NotificationAction): void {
  const capabilities = getPlatformCapabilities()
  if (capabilities.canNotification && window.electronAPI?.showNotification) {
    window.electronAPI.showNotification({
      title: message,
      body: '',
      sessionCode: '',
      eventName: 'ConnectionStatus',
      broadcastTs: Date.now(),
      action: action.kind === 'open-host'
        ? { kind: 'open-host', hostId: action.hostId }
        : { kind: 'open-session', hostId: action.hostId, sessionCode: action.sessionCode },
    })
  } else if ('Notification' in window && Notification.permission === 'granted') {
    const n = new Notification(message)
    n.onclick = () => handleNotificationClick(action)
  }
}
