import { useEffect } from 'react'
import SessionPanel from './components/SessionPanel'
import TerminalView from './components/TerminalView'
import { useSessionStore } from './stores/useSessionStore'

// TODO: daemonBase should come from host management (localStorage)
const daemonBase = 'http://100.64.0.2:7860'
const wsBase = daemonBase.replace(/^http/, 'ws')

export default function App() {
  const { sessions, activeId, fetch } = useSessionStore()
  const active = sessions.find((s) => s.id === activeId)

  useEffect(() => {
    fetch(daemonBase)
  }, [fetch])

  return (
    <div className="h-screen bg-gray-950 text-gray-200 flex">
      <SessionPanel />
      <div className="flex-1 h-full overflow-hidden">
        {active ? (
          <TerminalView wsUrl={`${wsBase}/ws/terminal/${encodeURIComponent(active.name)}`} />
        ) : (
          <div className="flex items-center justify-center h-full">
            <p className="text-gray-400">Select a session</p>
          </div>
        )}
      </div>
    </div>
  )
}
