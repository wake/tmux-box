// spa/src/components/AskUserQuestion.tsx
import { useState } from 'react'
import { ChatCircleDots, Check, X } from '@phosphor-icons/react'

interface Props {
  question: string
  options: string[]
  multiSelect: boolean
  onSubmit: (answer: string) => void
  onCancel: () => void
}

export default function AskUserQuestion({ question, options, multiSelect, onSubmit, onCancel }: Props) {
  const [selected, setSelected] = useState<Set<string>>(new Set())

  function toggle(opt: string) {
    setSelected(prev => {
      const next = new Set(prev)
      if (multiSelect) {
        if (next.has(opt)) next.delete(opt)
        else next.add(opt)
      } else {
        next.clear()
        next.add(opt)
      }
      return next
    })
  }

  function handleSubmit() {
    const answer = [...selected].join(', ')
    onSubmit(answer)
  }

  return (
    <div className="rounded-xl border border-blue-600/40 bg-blue-950/20 px-4 py-3 mx-4 my-2">
      {/* Header */}
      <div className="flex items-center gap-2 mb-3">
        <ChatCircleDots size={18} weight="fill" className="text-blue-400 flex-shrink-0" />
        <p className="text-sm font-semibold text-blue-200">{question}</p>
      </div>

      {/* Options */}
      <div className="flex flex-col gap-1.5 mb-3">
        {options.map(opt => {
          const isSelected = selected.has(opt)
          return (
            <button
              key={opt}
              onClick={() => toggle(opt)}
              className={`flex items-center gap-2 px-3 py-2 rounded-lg text-sm text-left cursor-pointer transition-colors ${
                isSelected
                  ? 'bg-blue-600/30 border border-blue-500/50 text-blue-100'
                  : 'bg-gray-800/60 border border-gray-700/50 text-gray-300 hover:bg-gray-700/60'
              }`}
            >
              <span className={`w-4 h-4 rounded flex items-center justify-center flex-shrink-0 ${isSelected ? 'bg-blue-500' : 'bg-gray-700'}`}>
                {isSelected && <Check size={10} weight="bold" className="text-white" />}
              </span>
              {opt}
            </button>
          )
        })}
      </div>

      {/* Action buttons */}
      <div className="flex gap-2 justify-end">
        <button
          data-testid="cancel-btn"
          onClick={onCancel}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded-md text-sm font-medium bg-gray-800 text-gray-300 hover:bg-gray-700 cursor-pointer"
        >
          <X size={13} weight="bold" />
          Cancel
        </button>
        <button
          data-testid="submit-btn"
          onClick={handleSubmit}
          disabled={selected.size === 0}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded-md text-sm font-medium bg-blue-600 text-white hover:bg-blue-500 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
        >
          <Check size={13} weight="bold" />
          Submit
        </button>
      </div>
    </div>
  )
}
