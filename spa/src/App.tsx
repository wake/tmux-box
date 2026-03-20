// spa/src/App.tsx — v1 重構：ActivityBar + TabBar + TabContent + StatusBar
import { useEffect, useState, useCallback } from 'react'
import { ActivityBar } from './components/ActivityBar'
import { TabBar } from './components/TabBar'
import { TabContent } from './components/TabContent'
import { StatusBar } from './components/StatusBar'
import SettingsPanel from './components/SettingsPanel'
import { SessionPicker } from './components/SessionPicker'
import { useSessionStore } from './stores/useSessionStore'
import { useConfigStore } from './stores/useConfigStore'
import { useTabStore } from './stores/useTabStore'
import { useWorkspaceStore } from './stores/useWorkspaceStore'
import { useHostStore } from './stores/useHostStore'
import { useRelayWsManager } from './hooks/useRelayWsManager'
import { useSessionEventWs } from './hooks/useSessionEventWs'
import { useSessionTabSync } from './hooks/useSessionTabSync'
import { useHashRouting } from './hooks/useHashRouting'
import { createSessionTab, isStandaloneTab } from './types/tab'
import { getSessionName } from './lib/tab-helpers'
import { getTabRenderer } from './lib/tab-registry'
import { TabContextMenu, type ContextMenuAction } from './components/TabContextMenu'
import type { Tab } from './types/tab'

export default function App() {
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [sessionPickerOpen, setSessionPickerOpen] = useState(false)
  const [contextMenu, setContextMenu] = useState<{ tab: Tab; position: { x: number; y: number } } | null>(null)

  // Host store (replaces hardcoded daemonBase)
  const getDaemonBase = useHostStore((s) => s.getDaemonBase)
  const getWsBase = useHostStore((s) => s.getWsBase)
  const daemonBase = getDaemonBase('local')
  const wsBase = getWsBase('local')

  // Existing stores
  const sessions = useSessionStore((s) => s.sessions)
  const fetchSessions = useSessionStore((s) => s.fetch)
  const fetchConfig = useConfigStore((s) => s.fetch)

  // Tab store
  const tabs = useTabStore((s) => s.tabs)
  const tabOrder = useTabStore((s) => s.tabOrder)
  const activeTabId = useTabStore((s) => s.activeTabId)
  const addTab = useTabStore((s) => s.addTab)
  const dismissTab = useTabStore((s) => s.dismissTab)
  const setActiveTab = useTabStore((s) => s.setActiveTab)
  const getActiveTab = useTabStore((s) => s.getActiveTab)

  // Workspace store
  const workspaces = useWorkspaceStore((s) => s.workspaces)
  const activeWorkspaceId = useWorkspaceStore((s) => s.activeWorkspaceId)
  const setActiveWorkspace = useWorkspaceStore((s) => s.setActiveWorkspace)
  const addTabToWorkspace = useWorkspaceStore((s) => s.addTabToWorkspace)
  const removeTabFromWorkspace = useWorkspaceStore((s) => s.removeTabFromWorkspace)
  const findWorkspaceByTab = useWorkspaceStore((s) => s.findWorkspaceByTab)
  const setWorkspaceActiveTab = useWorkspaceStore((s) => s.setWorkspaceActiveTab)

  // --- Extracted hooks ---
  useRelayWsManager(wsBase)
  useSessionEventWs(wsBase, daemonBase)
  useSessionTabSync(sessions)
  useHashRouting(activeTabId, setActiveTab)

  // --- Derived state ---
  const activeTab = getActiveTab()

  // --- Bootstrap: fetch sessions + config ---
  useEffect(() => {
    fetchSessions(daemonBase)
    fetchConfig(daemonBase)
  }, [fetchSessions, fetchConfig, daemonBase])

  // --- Handlers ---

  const handleSelectWorkspace = useCallback((wsId: string) => {
    setActiveWorkspace(wsId)
    const ws = workspaces.find((w) => w.id === wsId)
    if (ws?.activeTabId) setActiveTab(ws.activeTabId)
    else if (ws?.tabs[0]) setActiveTab(ws.tabs[0])
  }, [workspaces, setActiveWorkspace, setActiveTab])

  const handleSelectTab = useCallback((tabId: string) => {
    setActiveTab(tabId)
    const ws = findWorkspaceByTab(tabId)
    if (ws) {
      setActiveWorkspace(ws.id)
      setWorkspaceActiveTab(ws.id, tabId)
    }
  }, [setActiveTab, findWorkspaceByTab, setActiveWorkspace, setWorkspaceActiveTab])

  const handleCloseTab = useCallback((tabId: string) => {
    const ws = findWorkspaceByTab(tabId)
    if (ws) removeTabFromWorkspace(ws.id, tabId)
    dismissTab(tabId)
  }, [findWorkspaceByTab, removeTabFromWorkspace, dismissTab])

  const handleAddTab = useCallback(() => {
    setSessionPickerOpen(true)
  }, [])

  const handleSessionSelect = useCallback((session: typeof sessions[0]) => {
    setSessionPickerOpen(false)
    useTabStore.getState().undismissSession(session.name)
    const existing = Object.values(tabs).find((t) => getSessionName(t) === session.name)
    if (existing) {
      setActiveTab(existing.id)
      return
    }
    const tab = createSessionTab({
      label: session.name,
      hostId: 'local',
      sessionName: session.name,
      viewMode: session.mode === 'stream' ? 'stream' : 'terminal',
    })
    addTab(tab)
    setActiveTab(tab.id)
    if (activeWorkspaceId) {
      addTabToWorkspace(activeWorkspaceId, tab.id)
      setWorkspaceActiveTab(activeWorkspaceId, tab.id)
    }
  }, [tabs, setActiveTab, addTab, activeWorkspaceId, addTabToWorkspace, setWorkspaceActiveTab])

  const handleContextMenu = useCallback((e: React.MouseEvent, tabId: string) => {
    e.preventDefault()
    const tab = tabs[tabId]
    if (tab) setContextMenu({ tab, position: { x: e.clientX, y: e.clientY } })
  }, [tabs])

  const handleMiddleClick = useCallback((tabId: string) => {
    const tab = tabs[tabId]
    if (tab && !tab.locked) handleCloseTab(tabId)
  }, [tabs, handleCloseTab])

  const handleContextAction = useCallback((action: ContextMenuAction) => {
    if (!contextMenu) return
    const { tab } = contextMenu
    const store = useTabStore.getState()
    switch (action) {
      case 'viewMode-terminal': store.setViewMode(tab.id, 'terminal'); break
      case 'viewMode-stream': store.setViewMode(tab.id, 'stream'); break
      case 'lock': store.lockTab(tab.id); break
      case 'unlock': store.unlockTab(tab.id); break
      case 'pin': store.pinTab(tab.id); break
      case 'unpin': store.unpinTab(tab.id); break
      case 'close': handleCloseTab(tab.id); break
      case 'closeOthers': {
        const toClose = tabOrder.filter((id) => id !== tab.id && !tabs[id]?.locked)
        toClose.forEach((id) => handleCloseTab(id))
        break
      }
      case 'closeRight': {
        const idx = tabOrder.indexOf(tab.id)
        const toClose = tabOrder.slice(idx + 1).filter((id) => !tabs[id]?.locked)
        toClose.forEach((id) => handleCloseTab(id))
        break
      }
      case 'reopenClosed': {
        const dismissed = store.dismissedSessions
        if (dismissed.length > 0) store.undismissSession(dismissed[dismissed.length - 1])
        break
      }
    }
  }, [contextMenu, tabs, tabOrder, handleCloseTab])

  // --- Derive visible tabs for display ---
  const activeWs = workspaces.find((w) => w.id === activeWorkspaceId)
  const visibleTabs: Tab[] = activeWs
    ? activeWs.tabs.map((id) => tabs[id]).filter(Boolean)
    : []

  const standaloneTabs = tabOrder
    .filter((id) => isStandaloneTab(id, workspaces))
    .map((id) => tabs[id])
    .filter(Boolean)

  const activeStandaloneTabId = activeTabId && isStandaloneTab(activeTabId, workspaces) ? activeTabId : null

  const displayTabs = activeStandaloneTabId
    ? [tabs[activeStandaloneTabId]].filter(Boolean)
    : visibleTabs

  // StatusBar info
  const statusHost = activeTab?.hostId === 'local' ? 'mlab' : activeTab?.hostId ?? null
  const statusSession = activeTab ? getSessionName(activeTab) ?? activeTab?.label : null
  const statusViewMode = activeTab?.viewMode ?? null
  const activeTabConfig = activeTab ? getTabRenderer(activeTab.type) : null
  const statusViewModes = activeTabConfig?.viewModes ?? null

  return (
    <div className="h-screen flex bg-[#0a0a1a] text-gray-200">
      <ActivityBar
        workspaces={workspaces}
        standaloneTabs={standaloneTabs}
        activeWorkspaceId={activeStandaloneTabId ? null : activeWorkspaceId}
        activeStandaloneTabId={activeStandaloneTabId}
        onSelectWorkspace={handleSelectWorkspace}
        onSelectStandaloneTab={handleSelectTab}
        onAddWorkspace={() => {}}
        onOpenSettings={() => setSettingsOpen(true)}
      />
      <div className="flex-1 flex flex-col min-w-0">
        <TabBar
          tabs={displayTabs}
          activeTabId={activeTabId}
          onSelectTab={handleSelectTab}
          onCloseTab={handleCloseTab}
          onAddTab={handleAddTab}
          onReorderTabs={(order) => useTabStore.getState().reorderTabs(order)}
          onMiddleClick={handleMiddleClick}
          onContextMenu={handleContextMenu}
        />
        <div className="flex-1 flex overflow-hidden">
          <TabContent
            activeTab={activeTab ?? null}
            allTabs={tabOrder.map((id) => tabs[id]).filter(Boolean)}
            wsBase={wsBase}
            daemonBase={daemonBase}
          />
        </div>
        <StatusBar
          hostName={statusHost}
          sessionName={statusSession}
          status={activeTab ? 'connected' : null}
          viewMode={statusViewMode}
          viewModes={statusViewModes}
          onViewModeChange={(vm) => {
            if (activeTabId) useTabStore.getState().setViewMode(activeTabId, vm)
          }}
        />
      </div>
      {settingsOpen && (
        <SettingsPanel
          daemonBase={daemonBase}
          onClose={() => setSettingsOpen(false)}
        />
      )}
      {sessionPickerOpen && (
        <SessionPicker
          sessions={sessions}
          existingTabSessionNames={Object.values(tabs).map((t) => getSessionName(t)).filter(Boolean) as string[]}
          onSelect={handleSessionSelect}
          onClose={() => setSessionPickerOpen(false)}
        />
      )}
      {contextMenu && (
        <TabContextMenu
          tab={contextMenu.tab}
          position={contextMenu.position}
          onClose={() => setContextMenu(null)}
          onAction={handleContextAction}
          hasOtherUnlocked={tabOrder.some((id) => id !== contextMenu.tab.id && !tabs[id]?.locked)}
          hasRightUnlocked={tabOrder.slice(tabOrder.indexOf(contextMenu.tab.id) + 1).some((id) => !tabs[id]?.locked)}
          hasDismissedSessions={useTabStore.getState().dismissedSessions.length > 0}
        />
      )}
    </div>
  )
}
