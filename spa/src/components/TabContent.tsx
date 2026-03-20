import { getTabRenderer } from '../lib/tab-registry'
import type { Tab } from '../types/tab'

interface Props {
  activeTab: Tab | null
  wsBase: string
  daemonBase: string
}

export function TabContent({ activeTab, wsBase, daemonBase }: Props) {
  if (!activeTab) {
    return (
      <div className="flex-1 flex items-center justify-center text-gray-600 text-sm">
        選擇或建立一個分頁開始使用
      </div>
    )
  }

  const config = getTabRenderer(activeTab.type)
  if (config) {
    const Renderer = config.component
    return <Renderer tab={activeTab} isActive={true} wsBase={wsBase} daemonBase={daemonBase} />
  }

  return (
    <div className="flex-1 flex items-center justify-center text-gray-500 text-sm">
      Unknown tab type: {activeTab.type}
    </div>
  )
}
