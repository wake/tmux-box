// spa/src/App.tsx — v1 重構：ActivityBar + TabBar + TabContent + StatusBar
import { useEffect, useState, useCallback } from 'react'
import { ActivityBar } from './components/ActivityBar'
import { TabBar } from './components/TabBar'
import { TabContent } from './components/TabContent'
import { StatusBar } from './components/StatusBar'
import SettingsPanel from './components/SettingsPanel'
import { SessionPicker } from './components/SessionPicker'
import { useSessionStore } from './stores/useSessionStore'
import { useStreamStore } from './stores/useStreamStore'
import { useConfigStore } from './stores/useConfigStore'
import { useTabStore } from './stores/useTabStore'
import { useWorkspaceStore } from './stores/useWorkspaceStore'
import { useHostStore } from './stores/useHostStore'
import { useRelayWsManager } from './hooks/useRelayWsManager'
import { useSessionEventWs } from './hooks/useSessionEventWs'
import { useSessionTabSync } from './hooks/useSessionTabSync'
import { useHashRouting } from './hooks/useHashRouting'
import { handoff } from './lib/api'
import { createTab, isStandaloneTab } from './types/tab'
import type { Tab } from './types/tab'

export default function App() {
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [sessionPickerOpen, setSessionPickerOpen] = useState(false)
  const [terminalKey, setTerminalKey] = useState(0)
  const [terminalConnectMsg, setTerminalConnectMsg] = useState('')
  const [activePreset, setActivePreset] = useState('')

  // Host store (replaces hardcoded daemonBase)
  const getDaemonBase = useHostStore((s) => s.getDaemonBase)
  const getWsBase = useHostStore((s) => s.getWsBase)
  const daemonBase = getDaemonBase('local')
  const wsBase = getWsBase('local')

  // Existing stores
  const sessions = useSessionStore((s) => s.sessions)
  const fetchSessions = useSessionStore((s) => s.fetch)
  const config = useConfigStore((s) => s.config)
  const fetchConfig = useConfigStore((s) => s.fetch)

  // Tab store
  const tabs = useTabStore((s) => s.tabs)
  const tabOrder = useTabStore((s) => s.tabOrder)
  const activeTabId = useTabStore((s) => s.activeTabId)
  const addTab = useTabStore((s) => s.addTab)
  const dismissTab = useTabStore((s) => s.dismissTab)
  const setActiveTab = useTabStore((s) => s.setActiveTab)
  const updateTab = useTabStore((s) => s.updateTab)
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
  const activeSession = activeTab?.sessionName
    ? sessions.find((s) => s.name === activeTab.sessionName) ?? null
    : null
  const streamPresets = config?.stream?.presets ?? []

  // --- Bootstrap: fetch sessions + config ---
  useEffect(() => {
    fetchSessions(daemonBase)
    fetchConfig(daemonBase)
  }, [fetchSessions, fetchConfig, daemonBase])

  // --- Handlers ---

  const handleTerminalReconnect = useCallback(() => {
    setTerminalConnectMsg('正在套用新設定...')
    setTerminalKey((k) => k + 1)
    setTimeout(() => setTerminalConnectMsg(''), 5000)
  }, [])

  const handleHandoff = useCallback(
    async (mode?: string, preset?: string) => {
      if (!activeSession) return
      try {
        useStreamStore.getState().setHandoffProgress(activeSession.name, 'starting')
        await handoff(daemonBase, activeSession.id, mode ?? 'stream', preset)
        setActivePreset(preset ?? '')
        await fetchSessions(daemonBase)
      } catch (e) {
        console.error('Handoff failed:', e)
        useStreamStore.getState().setHandoffProgress(activeSession.name, '')
      }
    },
    [activeSession, daemonBase, fetchSessions],
  )

  const handleHandoffToTerm = useCallback(async () => {
    if (!activeSession) return
    try {
      useStreamStore.getState().setHandoffProgress(activeSession.name, 'starting')
      await handoff(daemonBase, activeSession.id, 'term')
      const tab = Object.values(tabs).find((t) => t.sessionName === activeSession.name)
      if (tab) {
        updateTab(tab.id, { type: 'terminal', icon: 'Terminal' })
      }
      setTerminalConnectMsg('正在切換到終端...')
      setTerminalKey((k) => k + 1)
      setTimeout(() => setTerminalConnectMsg(''), 5000)
      await fetchSessions(daemonBase)
    } catch (e) {
      console.error('Handoff to term failed:', e)
      useStreamStore.getState().setHandoffProgress(activeSession.name, '')
    }
  }, [activeSession, daemonBase, fetchSessions, tabs, updateTab])

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
    const existing = Object.values(tabs).find((t) => t.sessionName === session.name)
    if (existing) {
      setActiveTab(existing.id)
      return
    }
    const tab = createTab({
      type: session.mode === 'stream' ? 'stream' : 'terminal',
      label: session.name,
      hostId: 'local',
      sessionName: session.name,
    })
    addTab(tab)
    setActiveTab(tab.id)
    if (activeWorkspaceId) {
      addTabToWorkspace(activeWorkspaceId, tab.id)
      setWorkspaceActiveTab(activeWorkspaceId, tab.id)
    }
  }, [tabs, setActiveTab, addTab, activeWorkspaceId, addTabToWorkspace, setWorkspaceActiveTab])

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
  const statusSession = activeTab?.sessionName ?? null
  const statusMode = activeTab?.type ?? null

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
        />
        <div className="flex-1 flex overflow-hidden">
          <TabContent
            activeTab={activeTab ?? null}
            wsBase={wsBase}
            terminalKey={terminalKey}
            connectingMessage={terminalConnectMsg}
            onHandoff={() => handleHandoff('stream', activePreset || streamPresets[0]?.name || 'cc')}
            onHandoffToTerm={handleHandoffToTerm}
          />
        </div>
        <StatusBar
          hostName={statusHost}
          sessionName={statusSession}
          status={activeTab ? 'connected' : null}
          mode={statusMode}
        />
      </div>
      {settingsOpen && (
        <SettingsPanel
          daemonBase={daemonBase}
          onClose={() => setSettingsOpen(false)}
          onTerminalReconnect={handleTerminalReconnect}
        />
      )}
      {sessionPickerOpen && (
        <SessionPicker
          sessions={sessions}
          existingTabSessionNames={Object.values(tabs).map((t) => t.sessionName).filter(Boolean) as string[]}
          onSelect={handleSessionSelect}
          onClose={() => setSessionPickerOpen(false)}
        />
      )}
    </div>
  )
}
