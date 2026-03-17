// spa/src/components/StreamInput.tsx
import { useState, useRef, useCallback } from 'react'
import { Plus } from '@phosphor-icons/react'

interface Props {
  onSend: (text: string) => void
  onAttach?: () => void
  disabled?: boolean
  placeholder?: string
}

export default function StreamInput({ onSend, onAttach, disabled = false, placeholder = 'Reply...' }: Props) {
  const [value, setValue] = useState('')
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  const autoGrow = useCallback(() => {
    const ta = textareaRef.current
    if (ta) {
      ta.style.height = 'auto'
      ta.style.height = ta.scrollHeight + 'px'
    }
  }, [])

  function send() {
    const trimmed = value.trim()
    if (!trimmed) return
    onSend(trimmed)
    setValue('')
    if (textareaRef.current) {
      textareaRef.current.style.height = 'auto'
    }
  }

  function handleKeyDown(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      send()
    }
  }

  return (
    <div className={`mx-2 mb-2 border rounded-xl overflow-hidden transition-colors ${
      disabled ? 'opacity-40 border-[#404040] bg-[#242424]' : 'border-[#404040] bg-[#242424] focus-within:border-blue-400'
    }`}>
      <textarea
        ref={textareaRef}
        role="textbox"
        value={value}
        onChange={e => { setValue(e.target.value); autoGrow() }}
        onKeyDown={handleKeyDown}
        disabled={disabled}
        placeholder={placeholder}
        rows={1}
        className="w-full bg-transparent text-[#f5f5f5] placeholder-[#666] px-3 py-2.5 text-sm outline-none resize-none"
      />
      <div className="flex items-center px-2 pb-1.5">
        <button
          type="button"
          disabled={disabled}
          onClick={onAttach}
          className="w-7 h-7 rounded-md flex items-center justify-center text-[#666] hover:text-[#ddd] hover:bg-[#333] transition-colors disabled:opacity-40"
        >
          <Plus size={16} />
        </button>
      </div>
    </div>
  )
}
