// spa/src/components/ThinkingBlock.tsx
import { useState } from 'react'
import { Brain, CaretRight, CaretDown } from '@phosphor-icons/react'

interface Props {
  content: string
}

export default function ThinkingBlock({ content }: Props) {
  const [expanded, setExpanded] = useState(false)

  return (
    <div className="border-l-2 border-[#444] my-1">
      <button
        data-testid="thinking-header"
        className="flex items-center gap-2 px-2.5 py-1.5 text-xs text-[#888] hover:text-[#bbb] cursor-pointer w-full text-left"
        onClick={() => setExpanded(v => !v)}
      >
        <Brain size={14} />
        <span>Thinking...</span>
        <span className="ml-auto">
          {expanded ? <CaretDown size={10} /> : <CaretRight size={10} />}
        </span>
      </button>
      {expanded && (
        <div
          data-testid="thinking-content"
          className="px-2.5 pb-2 text-xs text-[#999] leading-relaxed whitespace-pre-wrap font-mono"
        >
          {content}
        </div>
      )}
    </div>
  )
}
