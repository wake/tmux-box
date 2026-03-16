import { useEffect } from 'react'
import SessionPanel from './components/SessionPanel'
import TerminalView from './components/TerminalView'
import { useSessionStore } from './stores/useSessionStore'

// TODO: daemonBase should come from host management
const daemonBase = 'http://localhost:7860'
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
      <div className="flex-1">
        {active ? (
          <TerminalView wsUrl={`${wsBase}/ws/terminal/${active.name}`} />
        ) : (
          <div className="flex items-center justify-center h-full">
            <p className="text-gray-500">Select a session</p>
          </div>
        )}
      </div>
    </div>
  )
}
