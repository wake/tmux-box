// spa/src/components/SessionPanel.tsx
import { useSessionStore } from '../stores/useSessionStore'
import { Terminal, Lightning, CircleDashed, GearSix } from '@phosphor-icons/react'

function SessionIcon({ mode, id }: { mode: string; id: number }) {
  const props = { size: 16, 'data-testid': `session-icon-${id}` }
  switch (mode) {
    case 'stream': return <Lightning {...props} weight="fill" className="text-blue-400" />
    case 'jsonl': return <CircleDashed {...props} className="text-yellow-400" />
    default: return <Terminal {...props} className="text-gray-400" />
  }
}

export default function SessionPanel() {
  const { sessions, activeId, setActive } = useSessionStore()

  return (
    <div className="w-56 bg-gray-900 border-r border-gray-800 flex flex-col">
      <div className="p-3 flex-1 overflow-y-auto">
        <h2 className="text-xs uppercase text-gray-400 mb-3">Sessions</h2>
        <div className="space-y-1">
          {sessions.length === 0 && <p className="text-sm text-gray-500">No sessions</p>}
          {sessions.map((s) => (
            <button
              key={s.id}
              onClick={() => setActive(s.id)}
              className={`w-full text-left px-2 py-1.5 rounded text-sm cursor-pointer flex items-center gap-2 ${
                activeId === s.id ? 'bg-gray-800 text-gray-100' : 'text-gray-400 hover:bg-gray-800/50'
              }`}
            >
              <SessionIcon mode={s.mode} id={s.id} />
              <span className="flex-1 truncate">{s.name}</span>
              <span className="text-xs text-gray-500">{s.mode}</span>
            </button>
          ))}
        </div>
      </div>
      {/* Settings button — fixed at bottom */}
      <div className="p-3 border-t border-gray-800">
        <button className="flex items-center gap-2 text-gray-400 hover:text-gray-300 text-sm cursor-pointer w-full">
          <GearSix size={16} />
          <span>Settings</span>
        </button>
      </div>
    </div>
  )
}
