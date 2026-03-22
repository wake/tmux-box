// spa/src/components/SessionPicker.tsx
import { useState, useRef, useEffect } from 'react'
import { Terminal, Lightning } from '@phosphor-icons/react'
import type { Session } from '../lib/api'

interface Props {
  sessions: Session[]
  existingTabSessionNames: string[]
  onSelect: (session: Session) => void
  onClose: () => void
}

export function SessionPicker({ sessions, existingTabSessionNames, onSelect, onClose }: Props) {
  const [search, setSearch] = useState('')
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    inputRef.current?.focus()
  }, [])

  const filtered = sessions.filter((s) =>
    s.name.toLowerCase().includes(search.toLowerCase()),
  )

  const hasTab = (name: string) => existingTabSessionNames.includes(name)

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center pt-[15vh]" onClick={onClose}>
      <div
        className="bg-[#1e1e3e] border border-gray-700 rounded-xl shadow-2xl w-[380px] max-h-[60vh] flex flex-col overflow-hidden"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Search */}
        <div className="p-3 border-b border-gray-700">
          <input
            ref={inputRef}
            type="text"
            placeholder="搜尋 session..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="w-full bg-[#0a0a1a] border border-gray-600 rounded-md px-3 py-2 text-sm text-white placeholder-gray-500 outline-none focus:border-purple-400"
          />
        </div>
        {/* List */}
        <div className="flex-1 overflow-y-auto">
          {filtered.map((s) => (
            <button
              key={s.code}
              onClick={() => onSelect(s)}
              className="w-full px-4 py-2.5 flex items-center gap-2 text-sm text-left hover:bg-[#2a2a5a] cursor-pointer transition-colors"
            >
              {s.mode === 'stream' ? <Lightning size={16} className="text-blue-400 flex-shrink-0" /> : <Terminal size={16} className="text-gray-400 flex-shrink-0" />}
              <span className="flex-1 text-gray-200">{s.name}</span>
              <span className="text-xs text-gray-600">{s.mode}</span>
              {hasTab(s.name) && <span className="text-xs text-purple-400">已開啟</span>}
            </button>
          ))}
          {filtered.length === 0 && (
            <div className="px-4 py-6 text-center text-gray-600 text-sm">無符合的 session</div>
          )}
        </div>
      </div>
    </div>
  )
}
