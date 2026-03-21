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
  iconMap: Record<string, React.ComponentType<{ size: number; className?: string }>>
}

export function SortableTab({ tab, isActive, pinned, onSelect, onClose, onMiddleClick, onContextMenu, iconMap }: Props) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: tab.id })

  const style = {
    transform: transform ? `translate3d(${Math.round(transform.x)}px, 0, 0)` : undefined,
    transition,
    zIndex: isDragging ? 10 : undefined,
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
        style={{ ...style, height: 26 }}
        {...attributes}
        {...listeners}
        onClick={() => onSelect(tab.id)}
        onMouseUp={handleMouseUp}
        onContextMenu={handleContextMenu}
        className={`relative flex items-center justify-center w-9 rounded-md cursor-pointer transition-all ${
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

  return (
    <button
      ref={setNodeRef}
      style={{ ...style, height: 26, maxWidth: 160 }}
      {...attributes}
      {...listeners}
      onClick={() => onSelect(tab.id)}
      onMouseUp={handleMouseUp}
      onContextMenu={handleContextMenu}
      className={`group flex items-center gap-1.5 px-3 text-xs whitespace-nowrap cursor-pointer transition-all rounded-md ${
        isActive
          ? 'text-white bg-[rgba(122,106,170,0.2)] border border-[rgba(122,106,170,0.3)]'
          : 'text-gray-500 hover:text-gray-300 hover:bg-[rgba(255,255,255,0.05)] border border-transparent'
      }`}
    >
      {IconComponent && <IconComponent size={14} className="flex-shrink-0" />}
      <span className="tab-label-fade overflow-hidden">{tab.label}</span>
      {isDirty(tab) && <span className="text-amber-400 text-[10px] flex-shrink-0">●</span>}
      {tab.locked && <Lock size={10} className="text-gray-600 ml-0.5 flex-shrink-0" />}
      {!tab.locked && (
        <button
          type="button"
          title="關閉分頁"
          onClick={(e) => { e.stopPropagation(); onClose(tab.id) }}
          className="ml-1 opacity-0 group-hover:opacity-100 hover:text-red-400 transition-opacity cursor-pointer flex-shrink-0"
        >
          <X size={12} />
        </button>
      )}
    </button>
  )
}
