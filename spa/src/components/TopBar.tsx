// spa/src/components/TopBar.tsx
import { Terminal, Lightning, CircleDashed, Stop } from '@phosphor-icons/react'

interface Props {
  sessionName: string
  mode: string
  onModeChange: (mode: string) => void
  onInterrupt: () => void
}

const modes = [
  { id: 'term', label: 'term', Icon: Terminal },
  { id: 'jsonl', label: 'jsonl', Icon: CircleDashed },
  { id: 'stream', label: 'stream', Icon: Lightning },
]

export default function TopBar({ sessionName, mode, onModeChange, onInterrupt }: Props) {
  return (
    <div className="h-10 bg-gray-800/80 border-b border-gray-700 flex items-center px-3 gap-3">
      <span className="text-sm text-gray-200 font-medium truncate">{sessionName}</span>

      <div className="flex-1" />

      {/* Mode buttons */}
      <div className="flex items-center gap-1" data-testid="mode-switch">
        {modes.map(({ id, label, Icon }) => (
          <button
            key={id}
            onClick={() => onModeChange(id)}
            data-testid={`mode-btn-${id}`}
            className={`flex items-center gap-1 px-2 py-1 rounded text-xs cursor-pointer transition-colors ${
              mode === id
                ? 'bg-gray-700 text-gray-100'
                : 'text-gray-500 hover:text-gray-300 hover:bg-gray-700/50'
            }`}
          >
            <Icon size={14} weight={mode === id ? 'fill' : 'regular'} />
            <span>{label}</span>
          </button>
        ))}
      </div>

      {/* Interrupt — stream mode only */}
      {mode === 'stream' && (
        <button
          data-testid="interrupt-btn"
          onClick={onInterrupt}
          className="flex items-center gap-1 px-2 py-1 rounded text-xs cursor-pointer text-red-400 hover:bg-gray-700"
        >
          <Stop size={14} weight="fill" />
          <span>Stop</span>
        </button>
      )}
    </div>
  )
}
