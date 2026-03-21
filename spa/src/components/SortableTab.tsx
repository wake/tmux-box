import { useSortable } from '@dnd-kit/sortable'
import { X, Lock } from '@phosphor-icons/react'
import type { Tab } from '../types/tab'
import { getTabIcon } from '../lib/tab-registry'
import { isDirty } from '../lib/tab-helpers'

interface Props {
  tab: Tab
  isActive: boolean
  pinned?: boolean
  onSelect: (tabId: string) => void
  onClose: (tabId: string) => void
  onMiddleClick: (tabId: string) => void
  onContextMenu: (e: React.MouseEvent, tabId: string) => void
  onHover?: (tabId: string | null) => void
  iconMap: Record<string, React.ComponentType<{ size: number; className?: string }>>
}

// Composite bg colors (pre-computed for opaque X button bg)
const TAB_BG_INACTIVE = '#12122a'
const TAB_BG_ACTIVE = '#1e1935'

export function SortableTab({ tab, isActive, pinned, onSelect, onClose, onMiddleClick, onContextMenu, onHover, iconMap }: Props) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: tab.id })

  const style = {
    transform: transform ? `translate3d(${Math.round(transform.x)}px, 0, 0)` : undefined,
    transition,
    zIndex: isDragging ? 10 : undefined,
  }

  const iconName = getTabIcon(tab)
  const IconComponent = iconMap[iconName]

  const handleMouseEnter = () => onHover?.(tab.id)
  const handleMouseLeave = () => onHover?.(null)
  const handleMouseUp = (e: React.MouseEvent) => {
    if (e.button === 1) { e.preventDefault(); onMiddleClick(tab.id) }
  }
  const handleContextMenu = (e: React.MouseEvent) => {
    e.preventDefault()
    onContextMenu(e, tab.id)
  }

  const tabBg = isActive ? TAB_BG_ACTIVE : TAB_BG_INACTIVE

  if (pinned) {
    return (
      <button
        ref={setNodeRef}
        style={{ ...style, height: 26, margin: '0 1px', marginTop: 2 }}
        {...attributes}
        {...listeners}
        onClick={() => onSelect(tab.id)}
        onMouseUp={handleMouseUp}
        onMouseEnter={handleMouseEnter}
        onMouseLeave={handleMouseLeave}
        onContextMenu={handleContextMenu}
        className={`relative flex items-center justify-center w-9 rounded-[6px] cursor-pointer transition-colors duration-150 ease-out ${
          isActive
            ? 'text-white bg-[rgba(122,106,170,0.2)] border border-[rgba(122,106,170,0.3)]'
            : 'text-gray-500 hover:text-gray-300 hover:bg-[rgba(255,255,255,0.05)] border border-transparent'
        }`}
        title={tab.label}
      >
        {IconComponent && <IconComponent size={14} className="flex-shrink-0" />}
      </button>
    )
  }

  const showClose = !tab.locked

  return (
    <button
      ref={setNodeRef}
      style={{ ...style, height: 26, margin: '0 1px', marginTop: 2, flex: '1 1 0px', maxWidth: 200, minWidth: 50 }}
      {...attributes}
      {...listeners}
      onClick={() => onSelect(tab.id)}
      onMouseUp={handleMouseUp}
      onMouseEnter={handleMouseEnter}
      onMouseLeave={handleMouseLeave}
      onContextMenu={handleContextMenu}
      className={`group relative flex items-center gap-1.5 pl-2 pr-1 text-xs whitespace-nowrap cursor-pointer transition-colors duration-150 ease-out rounded-[6px] overflow-hidden ${
        isActive
          ? 'text-white bg-[rgba(122,106,170,0.2)] border border-[rgba(122,106,170,0.3)]'
          : 'text-gray-500 hover:text-gray-300 hover:bg-[rgba(255,255,255,0.05)] border border-transparent'
      }`}
    >
      {IconComponent && <IconComponent size={14} className="flex-shrink-0" />}
      <span className="overflow-hidden flex-1 min-w-0">{tab.label}</span>
      {isDirty(tab) && <span className="text-amber-400 text-[10px] flex-shrink-0">●</span>}
      {tab.locked && <Lock size={10} className="text-gray-600 ml-0.5 flex-shrink-0" />}
      {showClose && (
        <span className="absolute right-0 top-0 bottom-0 flex items-center">
          {/* ② Gradient fade — always visible */}
          <span className="w-3 self-stretch" style={{ background: `linear-gradient(to right, transparent, ${tabBg})` }} />
          {/* ③ X button — collapses to 0 width when hidden */}
          <span
            title="關閉分頁"
            role="button"
            onClick={(e) => { e.stopPropagation(); onClose(tab.id) }}
            className={`self-stretch flex items-center overflow-hidden cursor-pointer rounded-r-[6px] transition-all duration-150 ease-out ${
              isActive
                ? 'w-6 opacity-100'
                : 'w-0 opacity-0 group-hover:w-6 group-hover:opacity-100'
            }`}
            style={{ backgroundColor: tabBg }}
          >
            <X size={12} className="mx-auto flex-shrink-0" />
          </span>
        </span>
      )}
    </button>
  )
}
