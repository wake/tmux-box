import { DndContext, closestCenter, PointerSensor, useSensor, useSensors, type DragEndEvent } from '@dnd-kit/core'
import { SortableContext, horizontalListSortingStrategy } from '@dnd-kit/sortable'
import { Plus, CaretLeft, CaretRight, Terminal, ChatCircleDots, File as FileIcon } from '@phosphor-icons/react'
import { SortableTab } from './SortableTab'
import { useScrollOverflow } from '../hooks/useScrollOverflow'
import type { Tab } from '../types/tab'

const ICON_MAP: Record<string, React.ComponentType<{ size: number; className?: string }>> = {
  Terminal,
  ChatCircleDots,
  File: FileIcon,
}

interface Props {
  tabs: Tab[]
  activeTabId: string | null
  onSelectTab: (tabId: string) => void
  onCloseTab: (tabId: string) => void
  onAddTab: () => void
  onReorderTabs: (newOrder: string[]) => void
  onMiddleClick: (tabId: string) => void
  onContextMenu: (e: React.MouseEvent, tabId: string) => void
}

function TabSeparator({ show }: { show: boolean }) {
  return <div className={`w-px h-3.5 flex-shrink-0 transition-opacity ${show ? 'bg-gray-700' : 'bg-transparent'}`} />
}

export function TabBar({ tabs, activeTabId, onSelectTab, onCloseTab, onAddTab, onReorderTabs, onMiddleClick, onContextMenu }: Props) {
  const pinnedTabs = tabs.filter((t) => t.pinned)
  const normalTabs = tabs.filter((t) => !t.pinned)
  const pinnedIds = pinnedTabs.map((t) => t.id)
  const normalIds = normalTabs.map((t) => t.id)
  const { containerRef: normalZoneRef, canScrollLeft, canScrollRight, scrollLeft, scrollRight } = useScrollOverflow()
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }))

  const handleDragEnd = (event: DragEndEvent) => {
    const { active, over } = event
    if (!over || active.id === over.id) return

    const activeId = String(active.id)
    const overId = String(over.id)

    const inPinned = pinnedIds.includes(activeId)
    const overInPinned = pinnedIds.includes(overId)

    // Only allow same-zone reorder
    if (inPinned !== overInPinned) return

    const zone = inPinned ? [...pinnedIds] : [...normalIds]
    const oldIdx = zone.indexOf(activeId)
    const newIdx = zone.indexOf(overId)
    zone.splice(oldIdx, 1)
    zone.splice(newIdx, 0, activeId)

    const newOrder = inPinned ? [...zone, ...normalIds] : [...pinnedIds, ...zone]
    onReorderTabs(newOrder)
  }

  // Separator visibility: hide between active tab and its neighbors
  const shouldShowSeparator = (leftTab: Tab | undefined, rightTab: Tab | undefined) => {
    if (!leftTab || !rightTab) return false
    if (leftTab.id === activeTabId || rightTab.id === activeTabId) return false
    return true
  }

  return (
    <div className="flex bg-[#12122a] border-b border-gray-800 h-9 items-center px-1 flex-shrink-0">
      <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={handleDragEnd}>
        {/* Pinned zone */}
        {pinnedTabs.length > 0 && (
          <>
            <SortableContext items={pinnedIds} strategy={horizontalListSortingStrategy}>
              <div className="flex items-center h-full">
                {pinnedTabs.map((tab, i) => (
                  <div key={tab.id} className="flex items-center h-full">
                    {i > 0 && <TabSeparator show={shouldShowSeparator(pinnedTabs[i - 1], tab)} />}
                    <SortableTab
                      tab={tab}
                      isActive={tab.id === activeTabId}
                      pinned
                      onSelect={onSelectTab}
                      onClose={onCloseTab}
                      onMiddleClick={onMiddleClick}
                      onContextMenu={onContextMenu}
                      iconMap={ICON_MAP}
                    />
                  </div>
                ))}
              </div>
            </SortableContext>
            <div className="w-px h-4 bg-gray-700 mx-1 flex-shrink-0" />
          </>
        )}

        {/* Normal zone with overflow arrows */}
        <div className="relative flex-1 min-w-0 h-full">
          {canScrollLeft && (
            <button
              onClick={scrollLeft}
              className="absolute left-0 top-0 bottom-0 z-10 w-8 flex items-center justify-center bg-gradient-to-r from-[#12122a] to-transparent cursor-pointer"
              aria-label="向左捲動"
            >
              <CaretLeft size={14} className="text-gray-400" />
            </button>
          )}
          <div ref={normalZoneRef} className="flex items-center h-full overflow-x-auto scrollbar-hide">
            <SortableContext items={normalIds} strategy={horizontalListSortingStrategy}>
              {normalTabs.map((tab, i) => (
                <div key={tab.id} className="flex items-center h-full">
                  {i > 0 && <TabSeparator show={shouldShowSeparator(normalTabs[i - 1], tab)} />}
                  <SortableTab
                    tab={tab}
                    isActive={tab.id === activeTabId}
                    onSelect={onSelectTab}
                    onClose={onCloseTab}
                    onMiddleClick={onMiddleClick}
                    onContextMenu={onContextMenu}
                    iconMap={ICON_MAP}
                  />
                </div>
              ))}
            </SortableContext>
          </div>
          {canScrollRight && (
            <button
              onClick={scrollRight}
              className="absolute right-0 top-0 bottom-0 z-10 w-8 flex items-center justify-center bg-gradient-to-l from-[#12122a] to-transparent cursor-pointer"
              aria-label="向右捲動"
            >
              <CaretRight size={14} className="text-gray-400" />
            </button>
          )}
        </div>
      </DndContext>

      <button
        onClick={onAddTab}
        className="flex items-center justify-center w-7 h-7 text-gray-600 hover:text-gray-400 cursor-pointer flex-shrink-0"
        title="新增分頁"
      >
        <Plus size={14} />
      </button>
    </div>
  )
}
