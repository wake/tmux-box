import { getTabRenderer } from '../lib/tab-registry'
import { useTabAlivePool } from '../hooks/useTabAlivePool'
import type { Tab } from '../types/tab'

interface Props {
  activeTab: Tab | null
  allTabs: Tab[]
  wsBase: string
  daemonBase: string
}

export function TabContent({ activeTab, allTabs, wsBase, daemonBase }: Props) {
  const { aliveIds, poolVersion } = useTabAlivePool(
    activeTab?.id ?? null,
    allTabs.map((t) => ({ id: t.id, pinned: t.pinned })),
  )

  const tabMap = new Map(allTabs.map((t) => [t.id, t]))
  const hasAliveTab = aliveIds.some((id) => tabMap.has(id))

  if (!activeTab && !hasAliveTab) {
    return (
      <div className="flex-1 flex items-center justify-center text-gray-600 text-sm">
        選擇或建立一個分頁開始使用
      </div>
    )
  }

  return (
    <div className="flex-1 relative">
      {aliveIds.map((id) => {
        const tab = tabMap.get(id)
        if (!tab) return null
        const config = getTabRenderer(tab.type)
        if (!config) {
          if (import.meta.env.DEV) console.warn(`No renderer for tab type: ${tab.type}`)
          return null
        }
        const Renderer = config.component
        const isActive = id === activeTab?.id
        return (
          <div
            key={`${id}-${poolVersion}`}
            className="absolute inset-0"
            style={{ display: isActive ? 'block' : 'none' }}
          >
            <Renderer tab={tab} isActive={isActive} wsBase={wsBase} daemonBase={daemonBase} />
          </div>
        )
      })}
    </div>
  )
}
