import { X, Plus, Terminal, ChatCircleDots, File as FileIcon } from '@phosphor-icons/react'
import type { Tab } from '../types/tab'

interface Props {
  tabs: Tab[]
  activeTabId: string | null
  onSelectTab: (tabId: string) => void
  onCloseTab: (tabId: string) => void
  onAddTab: () => void
}

const ICON_MAP: Record<string, React.ComponentType<{ size: number; className?: string }>> = {
  Terminal,
  ChatCircleDots,
  File: FileIcon,
}

function TabIcon({ icon, size = 14 }: { icon: string; size?: number }) {
  const Component = ICON_MAP[icon]
  if (!Component) return null
  return <Component size={size} className="flex-shrink-0" />
}

export function TabBar({ tabs, activeTabId, onSelectTab, onCloseTab, onAddTab }: Props) {
  return (
    <div className="flex bg-[#12122a] border-b border-gray-800 h-9 items-center px-1 gap-0.5 overflow-x-auto flex-shrink-0">
      {tabs.map((tab) => {
        const isActive = tab.id === activeTabId
        return (
          <button
            key={tab.id}
            onClick={() => onSelectTab(tab.id)}
            className={`group flex items-center gap-1.5 px-3 h-full text-xs whitespace-nowrap cursor-pointer transition-colors ${
              isActive
                ? 'text-white border-b-2 border-purple-400'
                : 'text-gray-500 hover:text-gray-300'
            }`}
          >
            <TabIcon icon={tab.icon} />
            <span>{tab.label}</span>
            {tab.isDirty && <span className="text-amber-400 text-[10px]">●</span>}
            <span
              title="關閉分頁"
              onClick={(e) => { e.stopPropagation(); onCloseTab(tab.id) }}
              className="ml-1 opacity-0 group-hover:opacity-100 hover:text-red-400 transition-opacity"
            >
              <X size={12} />
            </span>
          </button>
        )
      })}
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
