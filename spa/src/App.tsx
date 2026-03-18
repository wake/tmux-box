import { useEffect, useCallback, useState } from 'react'
import SessionPanel from './components/SessionPanel'
import TerminalView from './components/TerminalView'
import ConversationView from './components/ConversationView'
import TopBar from './components/TopBar'
import SettingsPanel from './components/SettingsPanel'
import { useSessionStore } from './stores/useSessionStore'
import { useStreamStore } from './stores/useStreamStore'
import { useConfigStore } from './stores/useConfigStore'
import { connectSessionEvents } from './lib/session-events'
import { handoff, fetchHistory } from './lib/api'
import { useRelayWsManager } from './hooks/useRelayWsManager'
import type { SessionStatus } from './components/SessionStatusBadge'

// TODO: daemonBase should come from host management (localStorage)
const daemonBase = 'http://100.64.0.2:7860'
const wsBase = daemonBase.replace(/^http/, 'ws')

// --- Hash Router: #/{uid}/{mode} ---
// Uses short auto-generated UID (stable across renames, URL-safe)

function parseHash(): { uid: string | null; mode: string } {
  const hash = window.location.hash.replace(/^#\/?/, '')
  if (!hash) return { uid: null, mode: 'term' }
  const parts = hash.split('/')
  const uid = parts[0] || null
  const mode = parts[1] || 'term'
  return { uid, mode }
}

function setHash(uid: string, mode: string) {
  const newHash = `#/${uid}/${mode}`
  if (window.location.hash !== newHash) {
    window.location.hash = newHash
  }
}

export default function App() {
  const { sessions, fetch: fetchSessions } = useSessionStore()
  const { config, fetch: fetchConfig } = useConfigStore()
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [activePreset, setActivePreset] = useState('')

  // Hash-based routing state
  const [route, setRoute] = useState(parseHash)

  // Listen for hash changes (browser back/forward)
  useEffect(() => {
    function onHashChange() {
      setRoute(parseHash())
    }
    window.addEventListener('hashchange', onHashChange)
    return () => window.removeEventListener('hashchange', onHashChange)
  }, [])

  // Derive active session from route (by UID)
  const active = sessions.find((s) => s.uid === route.uid) || null
  const currentMode = route.mode

  // Relay WS lifecycle — driven by relayStatus from session-events
  useRelayWsManager(wsBase)

  // Fetch sessions and config on mount
  useEffect(() => {
    fetchSessions(daemonBase)
    fetchConfig(daemonBase)
  }, [fetchSessions, fetchConfig])

  // Connect session-events WS on mount
  useEffect(() => {
    const conn = connectSessionEvents(
      `${wsBase}/ws/session-events`,
      (event) => {
        if (event.type === 'status') {
          useStreamStore.getState().setSessionStatus(event.session, event.value as SessionStatus)
          fetchSessions(daemonBase)
        }
        if (event.type === 'relay') {
          useStreamStore.getState().setRelayStatus(event.session, event.value === 'connected')
        }
        if (event.type === 'handoff') {
          const store = useStreamStore.getState()
          if (event.value === 'connected') {
            // Handoff completed — clear progress, fetch fresh data + history
            store.setHandoffProgress(event.session, '')
            fetchSessions(daemonBase).then(() => {
              const sess = useSessionStore.getState().sessions.find((s) => s.name === event.session)
              if (sess && sess.mode !== 'term') {
                fetchHistory(daemonBase, sess.id).then((msgs) => {
                  useStreamStore.getState().loadHistory(event.session, msgs)
                }).catch(() => { /* history fetch failed — non-critical */ })
              } else {
                // Term handoff — clear stale per-session state
                useStreamStore.getState().clearSession(event.session)
              }
            }).catch(() => { /* fetchSessions failed — non-critical */ })
          } else if (event.value.startsWith('failed')) {
            store.setHandoffProgress(event.session, '')
            fetchSessions(daemonBase)
          } else {
            // Progress update (detecting, stopping-cc, etc.)
            store.setHandoffProgress(event.session, event.value)
          }
        }
      },
    )
    return () => conn.close()
  }, [fetchSessions])

  // Select session → update hash
  const handleSelectSession = useCallback((id: number) => {
    const sess = sessions.find((s) => s.id === id)
    if (sess) setHash(sess.uid, 'term')
  }, [sessions])

  // Switch mode via hash (term button)
  const handleModeChange = useCallback((newMode: string) => {
    if (!active) return
    setHash(active.uid, newMode)
  }, [active])

  // Handoff for stream modes — stay on stream page, handoff runs in background
  // Progress is tracked via handoff events; relay connection drives UI state
  const handleHandoff = useCallback(async (mode: string, preset: string) => {
    if (!active) return
    setActivePreset(preset)
    try {
      useStreamStore.getState().setHandoffProgress(active.name, 'starting')
      await handoff(daemonBase, active.id, mode, preset)
      await fetchSessions(daemonBase)
    } catch (e) {
      console.error('handoff failed:', e)
      useStreamStore.getState().setHandoffProgress(active.name, '')
    }
  }, [active, fetchSessions])

  const handleHandoffToTerm = useCallback(async () => {
    if (!active) return
    setHash(active.uid, 'term')
    try {
      useStreamStore.getState().setHandoffProgress(active.name, 'starting')
      await handoff(daemonBase, active.id, 'term')
      await fetchSessions(daemonBase)
    } catch (e) {
      console.error('handoff to term failed:', e)
      useStreamStore.getState().setHandoffProgress(active.name, '')
    }
  }, [active, fetchSessions])

  // Derive presets from config
  const streamPresets = config?.stream?.presets || [{ name: 'cc', command: 'claude -p --verbose --input-format stream-json --output-format stream-json' }]

  return (
    <div className="h-screen bg-[#191919] text-gray-200 flex">
      <SessionPanel
        onSettingsOpen={() => setSettingsOpen(true)}
        onSelectSession={handleSelectSession}
        activeSessionUid={route.uid}
      />
      <div className="flex-1 flex flex-col h-full overflow-hidden">
        {active && (
          <TopBar
            sessionName={active.name}
            mode={currentMode}
            onModeChange={handleModeChange}
          />
        )}
        <div className="flex-1 overflow-hidden">
          {active ? (
            <>
              <div style={{ display: currentMode === 'term' ? 'block' : 'none', height: '100%' }}>
                <TerminalView
                  wsUrl={`${wsBase}/ws/terminal/${encodeURIComponent(active.name)}`}
                  visible={currentMode === 'term'}
                />
              </div>
              <div style={{
                display: currentMode === 'stream' ? 'flex' : 'none',
                flexDirection: 'column',
                height: '100%',
              }}>
                <ConversationView
                  sessionName={active.name}
                  onHandoff={() => handleHandoff('stream', activePreset || streamPresets[0]?.name || 'cc')}
                  onHandoffToTerm={handleHandoffToTerm}
                />
              </div>
            </>
          ) : (
            <div className="flex items-center justify-center h-full">
              <p className="text-gray-400">Select a session</p>
            </div>
          )}
        </div>
      </div>

      {settingsOpen && (
        <SettingsPanel
          daemonBase={daemonBase}
          onClose={() => setSettingsOpen(false)}
        />
      )}
    </div>
  )
}
