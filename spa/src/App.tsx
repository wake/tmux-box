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
import { handoff } from './lib/api'

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
  const conn = useStreamStore((s) => s.conn)
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
          useStreamStore.getState().setSessionStatus(event.session, event.value)
          fetchSessions(daemonBase)
        }
        if (event.type === 'handoff') {
          const { setHandoffState, setHandoffProgress } = useStreamStore.getState()
          if (event.value === 'connected') {
            setHandoffState('connected')
            setHandoffProgress('')
          } else if (event.value.startsWith('failed')) {
            setHandoffState('disconnected')
            setHandoffProgress('')
          } else {
            setHandoffProgress(event.value)
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

  // Handoff for stream modes → update hash immediately
  const handleHandoff = useCallback(async (mode: string, preset: string) => {
    if (!active) return
    setHash(active.uid, mode)
    setActivePreset(preset)
    try {
      useStreamStore.getState().setHandoffState('handoff-in-progress')
      await handoff(daemonBase, active.id, mode, preset)
      await fetchSessions(daemonBase)
    } catch (e) {
      console.error('handoff failed:', e)
      useStreamStore.getState().setHandoffState('disconnected')
    }
  }, [active, fetchSessions])

  const handleInterrupt = useCallback(() => {
    conn?.interrupt()
  }, [conn])

  // Derive presets from config
  const streamPresets = config?.stream?.presets || [{ name: 'cc', command: 'claude -p --input-format stream-json --output-format stream-json' }]

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
            streamPresets={streamPresets}
            activePreset={activePreset}
            onModeChange={handleModeChange}
            onHandoff={handleHandoff}
            onInterrupt={handleInterrupt}
          />
        )}
        <div className="flex-1 overflow-hidden">
          {active ? (
            currentMode === 'stream' ? (
              <ConversationView
                wsUrl={`${wsBase}/ws/cli-bridge-sub/${encodeURIComponent(active.name)}`}
                sessionName={active.name}
                presetName={activePreset || streamPresets[0]?.name || 'cc'}
                onHandoff={() => handleHandoff('stream', activePreset || streamPresets[0]?.name || 'cc')}
              />
            ) : (
              <TerminalView
                wsUrl={`${wsBase}/ws/terminal/${encodeURIComponent(active.name)}`}
              />
            )
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
