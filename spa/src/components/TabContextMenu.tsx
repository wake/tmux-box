import { useEffect, useLayoutEffect, useRef } from 'react'
import type { Tab } from '../types/tab'

export type ContextMenuAction =
  | 'viewMode-terminal' | 'viewMode-stream'
  | 'lock' | 'unlock' | 'pin' | 'unpin'
  | 'close' | 'closeOthers' | 'closeRight'
  | 'reopenClosed'

interface Props {
  tab: Tab
  position: { x: number; y: number }
  onClose: () => void
  onAction: (action: ContextMenuAction) => void
  hasOtherUnlocked: boolean
  hasRightUnlocked: boolean
  hasDismissedSessions: boolean
}

interface MenuItem {
  label: string
  action: ContextMenuAction
  show: boolean
  disabled?: boolean
}

export function TabContextMenu({ tab, position, onClose, onAction, hasOtherUnlocked, hasRightUnlocked, hasDismissedSessions }: Props) {
  const ref = useRef<HTMLDivElement>(null)

  // Viewport boundary correction — imperatively adjust position before paint
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    const rect = el.getBoundingClientRect()
    let { x, y } = position
    if (x + rect.width > window.innerWidth) x = window.innerWidth - rect.width - 4
    if (y + rect.height > window.innerHeight) y = window.innerHeight - rect.height - 4
    if (x < 0) x = 4
    if (y < 0) y = 4
    el.style.left = `${x}px`
    el.style.top = `${y}px`
  }, [position])

  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) onClose()
    }
    const escHandler = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    document.addEventListener('mousedown', handler)
    document.addEventListener('keydown', escHandler)
    return () => {
      document.removeEventListener('mousedown', handler)
      document.removeEventListener('keydown', escHandler)
    }
  }, [onClose])

  const isSession = tab.type === 'session'
  const items: (MenuItem | 'separator')[] = [
    // ViewMode section
    ...(isSession && tab.viewMode !== 'terminal' ? [{ label: '切換至 Terminal', action: 'viewMode-terminal' as const, show: true }] : []),
    ...(isSession && tab.viewMode !== 'stream' ? [{ label: '切換至 Stream', action: 'viewMode-stream' as const, show: true }] : []),
    ...(isSession ? ['separator' as const] : []),
    // Lock/Pin section
    { label: '鎖定分頁', action: 'lock' as const, show: !tab.locked },
    { label: '解鎖分頁', action: 'unlock' as const, show: tab.locked && !tab.pinned },
    { label: '固定分頁', action: 'pin' as const, show: !tab.pinned },
    { label: '取消固定', action: 'unpin' as const, show: tab.pinned },
    'separator',
    // Close section
    { label: '關閉分頁', action: 'close' as const, show: true, disabled: tab.locked },
    { label: '關閉其他分頁', action: 'closeOthers' as const, show: hasOtherUnlocked },
    { label: '關閉右側分頁', action: 'closeRight' as const, show: hasRightUnlocked },
    'separator',
    // Reopen
    { label: '重新開啟已關閉的分頁', action: 'reopenClosed' as const, show: hasDismissedSessions },
  ]

  const visibleItems = items.filter((item) => item === 'separator' || item.show)
  // Remove leading/trailing/consecutive separators
  const cleaned: typeof visibleItems = []
  for (const item of visibleItems) {
    if (item === 'separator') {
      if (cleaned.length > 0 && cleaned[cleaned.length - 1] !== 'separator') cleaned.push(item)
    } else {
      cleaned.push(item)
    }
  }
  if (cleaned[cleaned.length - 1] === 'separator') cleaned.pop()

  return (
    <div
      ref={ref}
      className="fixed z-50 bg-[#1e1e2e] border border-gray-700 rounded-lg shadow-xl py-1 min-w-[200px] text-xs"
      style={{ left: position.x, top: position.y }}
    >
      {cleaned.map((item, i) => {
        if (item === 'separator') {
          return <div key={`sep-${i}`} className="border-t border-gray-700 my-1" />
        }
        return (
          <button
            key={item.action}
            disabled={item.disabled}
            onClick={() => { onAction(item.action); onClose() }}
            className={`w-full text-left px-3 py-1.5 transition-colors ${
              item.disabled ? 'opacity-40 cursor-not-allowed' : 'cursor-pointer hover:bg-[#2a2a3e]'
            }`}
          >
            {item.label}
          </button>
        )
      })}
    </div>
  )
}
