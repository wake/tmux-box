import { Fragment, useRef, useState, useCallback, useMemo } from 'react'
import { DndContext, closestCenter, PointerSensor, useSensor, useSensors, type DragEndEvent, type Modifier } from '@dnd-kit/core'
import { SortableContext, horizontalListSortingStrategy } from '@dnd-kit/sortable'
import { Plus, CaretLeft, CaretRight, TerminalWindow, ChatCircleDots, File as FileIcon } from '@phosphor-icons/react'
import { SortableTab } from './SortableTab'
import { useScrollOverflow } from '../hooks/useScrollOverflow'
import type { Tab } from '../types/tab'

const TerminalWindowFill = (props: { size: number; className?: string }) => <TerminalWindow {...props} weight="fill" />

const ICON_MAP: Record<string, React.ComponentType<{ size: number; className?: string }>> = {
  TerminalWindow: TerminalWindowFill,
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
  return <div className={`w-px h-3.5 flex-shrink-0 transition-opacity duration-150 ease-out ${show ? 'bg-gray-700' : 'bg-transparent'}`} />
}

export function TabBar({ tabs, activeTabId, onSelectTab, onCloseTab, onAddTab, onReorderTabs, onMiddleClick, onContextMenu }: Props) {
  const pinnedTabs = tabs.filter((t) => t.pinned)
  const normalTabs = tabs.filter((t) => !t.pinned)
  const pinnedIds = useMemo(() => pinnedTabs.map((t) => t.id), [pinnedTabs])
  const normalIds = useMemo(() => normalTabs.map((t) => t.id), [normalTabs])
  const [hoveredTabId, setHoveredTabId] = useState<string | null>(null)
  const pinnedZoneRef = useRef<HTMLDivElement>(null)
  const normalZoneTabsRef = useRef<HTMLDivElement>(null)
  const { containerRef: normalZoneRef, canScrollLeft, canScrollRight, scrollLeft, scrollRight } = useScrollOverflow()
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }))

  // Custom modifier: restrict drag to the zone containing the active item
  const restrictToTabZone: Modifier = useCallback(({ transform, activeNodeRect, active }) => {
    if (!activeNodeRect || !active) return { ...transform, y: 0 }
    const activeId = String(active.id)
    const zone = pinnedIds.includes(activeId) ? pinnedZoneRef.current : normalZoneRef.current
    if (!zone) return { ...transform, y: 0 }
    const zoneRect = zone.getBoundingClientRect()
    const minX = zoneRect.left - activeNodeRect.left
    const maxX = zoneRect.right - activeNodeRect.right
    return { ...transform, x: Math.min(Math.max(transform.x, minX), maxX), y: 0 }
  }, [pinnedIds])

  const handleDragEnd = useCallback((event: DragEndEvent) => {
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
  }, [pinnedIds, normalIds, onReorderTabs])

  // Separator visibility: hide near active or hovered tab
  const shouldShowSeparator = (leftTab: Tab | undefined, rightTab: Tab | undefined) => {
    if (!leftTab || !rightTab) return false
    const hide = [activeTabId, hoveredTabId]
    if (hide.includes(leftTab.id) || hide.includes(rightTab.id)) return false
    return true
  }

  return (
    <div className="flex bg-[#12122a] border-b border-gray-800 items-center px-1 flex-shrink-0" style={{ height: 41 }}>
      <DndContext sensors={sensors} collisionDetection={closestCenter} modifiers={[restrictToTabZone]} onDragEnd={handleDragEnd}>
        {/* Pinned zone */}
        {pinnedTabs.length > 0 && (
          <>
            <SortableContext items={pinnedIds} strategy={horizontalListSortingStrategy}>
              <div ref={pinnedZoneRef} className="flex items-center h-full">
                {pinnedTabs.map((tab, i) => (
                  <Fragment key={tab.id}>
                    {i > 0 && <TabSeparator show={shouldShowSeparator(pinnedTabs[i - 1], tab)} />}
                    <SortableTab
                      tab={tab}
                      isActive={tab.id === activeTabId}
                      pinned
                      onSelect={onSelectTab}
                      onClose={onCloseTab}
                      onMiddleClick={onMiddleClick}
                      onContextMenu={onContextMenu}
                      onHover={setHoveredTabId}
                      iconMap={ICON_MAP}
                    />
                  </Fragment>
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
            <div ref={normalZoneTabsRef} className="flex items-center h-full">
              <SortableContext items={normalIds} strategy={horizontalListSortingStrategy}>
                {normalTabs.map((tab, i) => (
                  <Fragment key={tab.id}>
                    {i > 0 && <TabSeparator show={shouldShowSeparator(normalTabs[i - 1], tab)} />}
                    <SortableTab
                      tab={tab}
                      isActive={tab.id === activeTabId}
                      onSelect={onSelectTab}
                      onClose={onCloseTab}
                      onMiddleClick={onMiddleClick}
                      onContextMenu={onContextMenu}
                      onHover={setHoveredTabId}
                      iconMap={ICON_MAP}
                    />
                  </Fragment>
                ))}
              </SortableContext>
            </div>
            {/* Trailing separator + add button (outside SortableContext, inside scroll) */}
            {normalTabs.length > 0 && <TabSeparator show={(() => { const lastId = normalTabs[normalTabs.length - 1]?.id; return lastId !== activeTabId && lastId !== hoveredTabId })()} />}
            <button
              onClick={onAddTab}
              className="flex items-center justify-center w-7 h-7 text-gray-600 hover:text-gray-400 cursor-pointer flex-shrink-0"
              title="新增分頁"
              style={{ marginTop: 2 }}
            >
              <Plus size={14} />
            </button>
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
    </div>
  )
}
