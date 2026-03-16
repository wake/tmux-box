// spa/src/components/SessionPanel.tsx
import { useSessionStore } from '../stores/useSessionStore'

const modeIcon: Record<string, string> = { term: '❯', stream: '●', jsonl: '◐' }

export default function SessionPanel() {
  const { sessions, activeId, setActive } = useSessionStore()

  return (
    <div className="w-56 bg-gray-900 border-r border-gray-800 p-3 flex flex-col">
      <h2 className="text-xs uppercase text-gray-400 mb-3">Sessions</h2>
      <div className="flex-1 overflow-y-auto space-y-1">
        {sessions.length === 0 && <p className="text-sm text-gray-500">No sessions</p>}
        {sessions.map((s) => (
          <button
            key={s.id}
            onClick={() => setActive(s.id)}
            className={`w-full text-left px-2 py-1.5 rounded text-sm cursor-pointer ${
              activeId === s.id ? 'bg-gray-800 text-gray-100' : 'text-gray-400 hover:bg-gray-800/50'
            }`}
          >
            <span className="mr-1.5">{modeIcon[s.mode] ?? '❯'}</span>
            {s.name}
            <span className="float-right text-xs text-gray-500">{s.mode}</span>
          </button>
        ))}
      </div>
    </div>
  )
}
