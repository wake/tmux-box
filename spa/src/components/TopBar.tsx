// @deprecated Phase 1 — TopBar 功能已被 TabBar 取代。確認無其他引用後可刪除。
// spa/src/components/TopBar.tsx
import { Terminal, Lightning } from '@phosphor-icons/react'

interface Props {
  sessionName: string
  mode: string
  onModeChange: (mode: string) => void
}

export default function TopBar({ sessionName, mode, onModeChange }: Props) {
  return (
    <div className="h-10 bg-[#242424] border-b border-[#404040] flex items-center px-3 gap-3 relative">
      <span className="text-sm text-[#e5e5e5] font-medium truncate">{sessionName}</span>
      <div className="flex-1" />

      <div className="flex items-center gap-1" data-testid="mode-switch">
        {/* Term */}
        <button
          onClick={() => onModeChange('term')}
          data-testid="mode-btn-term"
          className={`flex items-center gap-1 px-2 py-1 rounded text-xs cursor-pointer transition-colors ${
            mode === 'term' ? 'bg-[#404040] text-[#f0f0f0]' : 'text-[#888] hover:text-[#ccc] hover:bg-[#333]'
          }`}
        >
          <Terminal size={14} weight={mode === 'term' ? 'fill' : 'regular'} />
          <span>term</span>
        </button>

        {/* Stream */}
        <button
          onClick={() => onModeChange('stream')}
          data-testid="mode-btn-stream"
          className={`flex items-center gap-1 px-2 py-1 rounded text-xs cursor-pointer transition-colors ${
            mode === 'stream' ? 'bg-[#404040] text-[#f0f0f0]' : 'text-[#888] hover:text-[#ccc] hover:bg-[#333]'
          }`}
        >
          <Lightning size={14} weight={mode === 'stream' ? 'fill' : 'regular'} />
          <span>stream</span>
        </button>
      </div>
    </div>
  )
}
