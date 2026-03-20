import { useSortable } from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
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
  iconMap: Record<string, React.ComponentType<{ size: number; className?: string }>>
}

export function SortableTab({ tab, isActive, pinned, onSelect, onClose, onMiddleClick, onContextMenu, iconMap }: Props) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: tab.id })

  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.5 : 1,
  }

  const iconName = getTabIcon(tab)
  const IconComponent = iconMap[iconName]

  const handleMouseUp = (e: React.MouseEvent) => {
    if (e.button === 1) { e.preventDefault(); onMiddleClick(tab.id) }
  }

  const handleContextMenu = (e: React.MouseEvent) => {
    e.preventDefault()
    onContextMenu(e, tab.id)
  }

  if (pinned) {
    return (
      <button
        ref={setNodeRef}
        style={style}
        {...attributes}
        {...listeners}
        onClick={() => onSelect(tab.id)}
        onMouseUp={handleMouseUp}
        onContextMenu={handleContextMenu}
        className={`relative flex items-center justify-center w-9 h-full cursor-pointer transition-colors ${
          isActive ? 'text-white border-b-2 border-purple-400' : 'text-gray-500 hover:text-gray-300'
        }`}
        title={tab.label}
      >
        {IconComponent && <IconComponent size={14} className="flex-shrink-0" />}
      </button>
    )
  }

  return (
    <button
      ref={setNodeRef}
      style={style}
      {...attributes}
      {...listeners}
      onClick={() => onSelect(tab.id)}
      onMouseUp={handleMouseUp}
      onContextMenu={handleContextMenu}
      className={`group flex items-center gap-1.5 px-3 h-full text-xs whitespace-nowrap cursor-pointer transition-colors ${
        isActive ? 'text-white border-b-2 border-purple-400' : 'text-gray-500 hover:text-gray-300'
      }`}
    >
      {IconComponent && <IconComponent size={14} className="flex-shrink-0" />}
      <span>{tab.label}</span>
      {isDirty(tab) && <span className="text-amber-400 text-[10px]">●</span>}
      {tab.locked && <Lock size={10} className="text-gray-600 ml-0.5" />}
      {!tab.locked && (
        <span
          title="關閉分頁"
          onClick={(e) => { e.stopPropagation(); onClose(tab.id) }}
          className="ml-1 opacity-0 group-hover:opacity-100 hover:text-red-400 transition-opacity"
        >
          <X size={12} />
        </span>
      )}
    </button>
  )
}
