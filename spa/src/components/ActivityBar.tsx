import { Plus, GearSix } from '@phosphor-icons/react'
import type { Tab, Workspace } from '../types/tab'

interface Props {
  workspaces: Workspace[]
  standaloneTabs: Tab[]
  activeWorkspaceId: string | null
  activeStandaloneTabId: string | null
  onSelectWorkspace: (wsId: string) => void
  onSelectStandaloneTab: (tabId: string) => void
  onAddWorkspace: () => void
  onOpenSettings: () => void
}

export function ActivityBar({
  workspaces,
  standaloneTabs,
  activeWorkspaceId,
  activeStandaloneTabId,
  onSelectWorkspace,
  onSelectStandaloneTab,
  onAddWorkspace,
  onOpenSettings,
}: Props) {
  return (
    <div className="hidden lg:flex w-11 flex-col items-center bg-[#08081a] border-r border-gray-800 py-2 gap-2 flex-shrink-0">
      {/* Workspaces */}
      {workspaces.map((ws) => (
        <button
          key={ws.id}
          title={ws.name}
          onClick={() => onSelectWorkspace(ws.id)}
          className={`w-8 h-8 rounded-md flex items-center justify-center text-xs cursor-pointer transition-all ${
            activeWorkspaceId === ws.id && !activeStandaloneTabId
              ? 'ring-2 ring-purple-400'
              : 'opacity-70 hover:opacity-100'
          }`}
          style={{ backgroundColor: ws.color + '33', color: ws.color }}
        >
          {ws.icon ?? ws.name.charAt(0)}
        </button>
      ))}

      {/* Separator */}
      {standaloneTabs.length > 0 && (
        <div className="w-5 h-px bg-gray-700 my-1" />
      )}

      {/* Standalone tabs */}
      {standaloneTabs.map((tab) => (
        <button
          key={tab.id}
          title={tab.label}
          onClick={() => onSelectStandaloneTab(tab.id)}
          className={`w-8 h-8 rounded-md flex items-center justify-center text-xs cursor-pointer transition-all ${
            activeStandaloneTabId === tab.id
              ? 'ring-2 ring-purple-400 bg-gray-800'
              : 'bg-gray-900 opacity-70 hover:opacity-100'
          }`}
        >
          {tab.label.charAt(0).toUpperCase()}
        </button>
      ))}

      {/* Add + Settings */}
      <div className="mt-auto flex flex-col items-center gap-2 pb-1">
        <button
          title="新增工作區"
          onClick={onAddWorkspace}
          className="w-8 h-8 rounded-md flex items-center justify-center text-gray-600 hover:text-gray-400 hover:bg-gray-800 cursor-pointer"
        >
          <Plus size={16} />
        </button>
        <button
          title="設定"
          onClick={onOpenSettings}
          className="w-8 h-8 rounded-md flex items-center justify-center text-gray-600 hover:text-gray-400 hover:bg-gray-800 cursor-pointer"
        >
          <GearSix size={16} />
        </button>
      </div>
    </div>
  )
}
