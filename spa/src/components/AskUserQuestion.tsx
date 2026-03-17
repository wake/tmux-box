// spa/src/components/AskUserQuestion.tsx
import { useState, useRef } from 'react'
import { ChatCircleDots, Check } from '@phosphor-icons/react'

export interface QuestionItem {
  question: string
  header?: string
  options?: Array<{ label: string; description?: string }>
  multiSelect?: boolean
}

interface Props {
  questions: QuestionItem[]
  onSubmit: (answer: string) => void
  onCancel: () => void
}

export default function AskUserQuestion({ questions, onSubmit, onCancel }: Props) {
  const q = questions[0] || { question: 'Please answer:', options: [], multiSelect: false }
  const options = q.options || []
  const multiSelect = q.multiSelect || false
  const isFreeText = options.length === 0

  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [text, setText] = useState('')
  const inputRef = useRef<HTMLInputElement>(null)

  function toggle(label: string) {
    setSelected(prev => {
      const next = new Set(prev)
      if (multiSelect) {
        if (next.has(label)) next.delete(label)
        else next.add(label)
      } else {
        next.clear()
        next.add(label)
      }
      return next
    })
  }

  function handleContainerKeyDown(e: React.KeyboardEvent) {
    if (e.key === 'Escape') {
      onCancel()
      return
    }
    if (e.key === 'Enter' && !isFreeText && selected.size > 0) {
      onSubmit([...selected].join(', '))
    }
  }

  function handleInputKeyDown(e: React.KeyboardEvent) {
    if (e.key === 'Escape') {
      onCancel()
      return
    }
    if (e.key === 'Enter' && text.trim()) {
      onSubmit(text.trim())
    }
  }

  return (
    <div
      data-testid="ask-container"
      tabIndex={-1}
      onKeyDown={handleContainerKeyDown}
      className="rounded-xl border border-blue-600/40 bg-blue-950/20 px-4 py-3 mx-4 my-2 outline-none"
    >
      {/* Header */}
      <div className="flex items-center gap-2 mb-3">
        <ChatCircleDots size={18} weight="fill" className="text-blue-400 flex-shrink-0" />
        <p className="text-sm font-semibold text-blue-200">{q.question}</p>
      </div>

      {isFreeText ? (
        /* Free-text input */
        <input
          ref={inputRef}
          type="text"
          value={text}
          onChange={e => setText(e.target.value)}
          onKeyDown={handleInputKeyDown}
          placeholder="Type your answer…"
          className="w-full rounded-lg border border-gray-700/50 bg-gray-800/60 px-3 py-2 text-sm text-gray-200 placeholder-gray-500 outline-none focus:border-blue-500/50"
          autoFocus
        />
      ) : (
        /* Options */
        <div className="flex flex-col gap-1.5">
          {options.map(opt => {
            const isSelected = selected.has(opt.label)
            return (
              <button
                key={opt.label}
                onClick={() => toggle(opt.label)}
                className={`flex items-center gap-2 px-3 py-2 rounded-lg text-sm text-left cursor-pointer transition-colors ${
                  isSelected
                    ? 'bg-blue-600/30 border border-blue-500/50 text-blue-100'
                    : 'bg-gray-800/60 border border-gray-700/50 text-gray-300 hover:bg-gray-700/60'
                }`}
              >
                <span className={`w-4 h-4 rounded flex items-center justify-center flex-shrink-0 ${isSelected ? 'bg-blue-500' : 'bg-gray-700'}`}>
                  {isSelected && <Check size={10} weight="bold" className="text-white" />}
                </span>
                <span>
                  {opt.label}
                  {opt.description && (
                    <span className="text-gray-400 text-xs ml-1.5">— {opt.description}</span>
                  )}
                </span>
              </button>
            )
          })}
        </div>
      )}
    </div>
  )
}
