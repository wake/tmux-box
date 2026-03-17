// spa/src/components/ToolCallBlock.tsx
import { useState } from 'react'
import {
  CaretRight,
  CaretDown,
  Terminal,
  File,
  PencilSimple,
  Globe,
  MagnifyingGlass,
} from '@phosphor-icons/react'

interface Props {
  tool: string
  input: Record<string, unknown>
}

function getToolIcon(tool: string) {
  switch (tool) {
    case 'Bash':
      return <Terminal size={14} className="text-amber-300" />
    case 'Read':
    case 'Write':
      return <File size={14} className="text-sky-300" />
    case 'Edit':
      return <PencilSimple size={14} className="text-emerald-300" />
    case 'WebFetch':
      return <Globe size={14} className="text-violet-300" />
    case 'Grep':
    case 'Glob':
      return <MagnifyingGlass size={14} className="text-gray-300" />
    default:
      return <Terminal size={14} className="text-gray-300" />
  }
}

function getSummary(tool: string, input: Record<string, unknown>): string {
  switch (tool) {
    case 'Bash':
      return (input.command as string) ?? ''
    case 'Read':
    case 'Write':
    case 'Edit':
      return (input.file_path as string) ?? ''
    case 'WebFetch':
      return (input.url as string) ?? ''
    case 'Grep':
      return (input.pattern as string) ?? ''
    case 'Glob':
      return (input.pattern as string) ?? ''
    default:
      return JSON.stringify(input).slice(0, 80)
  }
}

export default function ToolCallBlock({ tool, input }: Props) {
  const [expanded, setExpanded] = useState(false)
  const summary = getSummary(tool, input)

  return (
    <div className="rounded-lg border border-[#404040] bg-[#1e1e1e] text-sm my-1 overflow-hidden">
      {/* Header */}
      <button
        data-testid="tool-header"
        className="w-full flex items-center gap-2 px-3 py-2 hover:bg-[#2a2a2a] cursor-pointer text-left"
        onClick={() => setExpanded(v => !v)}
      >
        {expanded ? (
          <CaretDown size={12} className="text-gray-400 flex-shrink-0" />
        ) : (
          <CaretRight size={12} className="text-gray-400 flex-shrink-0" />
        )}
        {getToolIcon(tool)}
        <span className="text-[#e0e0e0] font-medium">{tool}</span>
        {summary && (
          <span className="text-[#888] truncate flex-1 min-w-0">{summary}</span>
        )}
      </button>

      {/* Detail */}
      {expanded && (
        <div
          data-testid="tool-detail"
          className="border-t border-[#404040] px-3 py-2 bg-[#161616]"
        >
          <pre className="text-xs text-[#ccc] whitespace-pre-wrap break-all overflow-auto max-h-60">
            {JSON.stringify(input, null, 2)}
          </pre>
        </div>
      )}
    </div>
  )
}
