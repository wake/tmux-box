// spa/src/components/TopBar.tsx
import { useState, useEffect } from 'react'
import { Terminal, Lightning } from '@phosphor-icons/react'

interface Preset {
  name: string
  command: string
}

interface Props {
  sessionName: string
  mode: string
  streamPresets: Preset[]
  activePreset?: string
  onModeChange: (mode: string) => void
  onHandoff: (mode: string, preset: string) => void
  onInterrupt: () => void
}

export default function TopBar({ sessionName, mode, streamPresets, activePreset, onModeChange, onHandoff }: Props) {
  const [openDropdown, setOpenDropdown] = useState<string | null>(null)

  // Click-outside + Escape to close dropdown
  useEffect(() => {
    if (!openDropdown) return
    function handleClick(e: MouseEvent) {
      if (!(e.target as Element).closest('[data-testid="mode-switch"]')) {
        setOpenDropdown(null)
      }
    }
    function handleKeyDown(e: KeyboardEvent) {
      if (e.key === 'Escape') setOpenDropdown(null)
    }
    document.addEventListener('mousedown', handleClick)
    document.addEventListener('keydown', handleKeyDown)
    return () => {
      document.removeEventListener('mousedown', handleClick)
      document.removeEventListener('keydown', handleKeyDown)
    }
  }, [openDropdown])

  function handleModeClick(m: string, presets: Preset[]) {
    if (m === 'term') {
      onModeChange('term')
      setOpenDropdown(null)
      return
    }
    if (presets.length <= 1) {
      onHandoff(m, presets[0]?.name || 'cc')
      setOpenDropdown(null)
      return
    }
    setOpenDropdown(openDropdown === m ? null : m)
  }

  function handlePresetClick(m: string, presetName: string) {
    onHandoff(m, presetName)
    setOpenDropdown(null)
  }

  return (
    <div className="h-10 bg-[#242424] border-b border-[#404040] flex items-center px-3 gap-3 relative">
      <span className="text-sm text-[#e5e5e5] font-medium truncate">{sessionName}</span>
      <div className="flex-1" />

      <div className="flex items-center gap-1" data-testid="mode-switch">
        {/* Term */}
        <button
          onClick={() => handleModeClick('term', [])}
          data-testid="mode-btn-term"
          className={`flex items-center gap-1 px-2 py-1 rounded text-xs cursor-pointer transition-colors ${
            mode === 'term' ? 'bg-[#404040] text-[#f0f0f0]' : 'text-[#888] hover:text-[#ccc] hover:bg-[#333]'
          }`}
        >
          <Terminal size={14} weight={mode === 'term' ? 'fill' : 'regular'} />
          <span>term</span>
        </button>

        {/* Stream — with dropdown */}
        <div className="relative">
          <button
            onClick={() => handleModeClick('stream', streamPresets)}
            data-testid="mode-btn-stream"
            className={`flex items-center gap-1 px-2 py-1 rounded text-xs cursor-pointer transition-colors ${
              mode === 'stream' ? 'bg-[#404040] text-[#f0f0f0]' : 'text-[#888] hover:text-[#ccc] hover:bg-[#333]'
            }`}
          >
            <Lightning size={14} weight={mode === 'stream' ? 'fill' : 'regular'} />
            <span>stream</span>
            {streamPresets.length > 1 && <span className="text-[10px]">&#9662;</span>}
          </button>
          {openDropdown === 'stream' && (
            <div data-testid="dropdown-stream" className="absolute top-full right-0 mt-1 bg-[#2a2a2a] border border-[#404040] rounded-lg py-1 min-w-[140px] z-10 shadow-lg">
              {streamPresets.map(p => (
                <button key={p.name} onClick={() => handlePresetClick('stream', p.name)}
                  className={`block w-full text-left px-3 py-1.5 text-xs ${
                    p.name === activePreset ? 'bg-[#404040] text-white' : 'text-[#ddd] hover:bg-[#404040]'
                  }`}>
                  {p.name}
                </button>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
