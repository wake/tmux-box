import { useEffect, useCallback } from 'react'
import SessionPanel from './components/SessionPanel'
import TerminalView from './components/TerminalView'
import ConversationView from './components/ConversationView'
import TopBar from './components/TopBar'
import { useSessionStore } from './stores/useSessionStore'
import { useStreamStore } from './stores/useStreamStore'
import { switchMode } from './lib/api'

// TODO: daemonBase should come from host management (localStorage)
const daemonBase = 'http://100.64.0.2:7860'
const wsBase = daemonBase.replace(/^http/, 'ws')

export default function App() {
  const { sessions, activeId, fetch } = useSessionStore()
  const conn = useStreamStore((s) => s.conn)
  const active = sessions.find((s) => s.id === activeId)

  useEffect(() => {
    fetch(daemonBase)
  }, [fetch])

  const handleModeSwitch = useCallback(async () => {
    if (!active) return
    const newMode = active.mode === 'stream' ? 'term' : 'stream'
    try {
      await switchMode(daemonBase, active.id, newMode)
      await fetch(daemonBase) // refresh sessions
    } catch (e) {
      console.error('mode switch failed:', e)
    }
  }, [active, fetch])

  const handleInterrupt = useCallback(() => {
    conn?.interrupt()
  }, [conn])

  return (
    <div className="h-screen bg-gray-950 text-gray-200 flex">
      <SessionPanel />
      <div className="flex-1 flex flex-col h-full overflow-hidden">
        {active && (
          <TopBar
            sessionName={active.name}
            mode={active.mode}
            onModeSwitch={handleModeSwitch}
            onInterrupt={handleInterrupt}
          />
        )}
        <div className="flex-1 overflow-hidden">
          {active ? (
            active.mode === 'stream' ? (
              <ConversationView
                wsUrl={`${wsBase}/ws/stream/${encodeURIComponent(active.name)}`}
                sessionName={active.name}
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
    </div>
  )
}
