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
import { connectSessionEvents } from './lib/session-events'
import { parseHash, setHash } from './lib/hash-routing'
import { handoff, fetchHistory } from './lib/api'
import { createTab, isStandaloneTab } from './types/tab'
import type { Tab } from './types/tab'
import type { SessionStatus } from './components/SessionStatusBadge'

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
  const removeTab = useTabStore((s) => s.removeTab)
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

  // Relay WS manager — creates stream WS connections driven by relay status
  useRelayWsManager(wsBase)

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

  // --- Session events WS (ported from original App.tsx) ---
  useEffect(() => {
    const conn = connectSessionEvents(
      `${wsBase}/ws/session-events`,
      (event) => {
        if (event.type === 'status') {
          useStreamStore.getState().setSessionStatus(event.session, event.value as SessionStatus)
          fetchSessions(daemonBase)
        }
        if (event.type === 'relay') {
          useStreamStore.getState().setRelayStatus(event.session, event.value === 'connected')
          // Note: history reload on relay reconnect deferred to Phase 3
        }
        if (event.type === 'handoff') {
          const store = useStreamStore.getState()
          if (event.value === 'connected') {
            // Handoff completed — clear progress, fetch fresh data + history
            store.setHandoffProgress(event.session, '')
            fetchSessions(daemonBase).then(() => {
              const sess = useSessionStore.getState().sessions.find((s) => s.name === event.session)
              if (sess && sess.mode !== 'term') {
                fetchHistory(daemonBase, sess.id).then((msgs) => {
                  useStreamStore.getState().loadHistory(event.session, msgs)
                }).catch(() => { /* history fetch failed — non-critical */ })
              } else {
                // Term handoff — clear stale per-session state
                useStreamStore.getState().clearSession(event.session)
              }
            }).catch(() => { /* fetchSessions failed — non-critical */ })
          } else if (event.value.startsWith('failed')) {
            store.setHandoffProgress(event.session, '')
            fetchSessions(daemonBase)
          } else {
            // Progress update (detecting, stopping-cc, etc.)
            store.setHandoffProgress(event.session, event.value)
          }
        }
      },
    )
    return () => conn.close()
  }, [fetchSessions, daemonBase, wsBase])

  // --- Auto tab sync: sessions → tabs (add new, remove stale) ---
  useEffect(() => {
    const sessionNames = new Set(sessions.map((s) => s.name))

    // Add tabs for new sessions
    sessions.forEach((s) => {
      const currentTabs = useTabStore.getState().tabs
      const existingTab = Object.values(currentTabs).find((t) => t.sessionName === s.name)
      if (!existingTab) {
        const tab = createTab({
          type: s.mode === 'stream' ? 'stream' : 'terminal',
          label: s.name,
          hostId: 'local',
          sessionName: s.name,
        })
        useTabStore.getState().addTab(tab)
        const defaultWsId = useWorkspaceStore.getState().workspaces[0]?.id
        if (defaultWsId) useWorkspaceStore.getState().addTabToWorkspace(defaultWsId, tab.id)
      }
    })

    // Remove tabs for sessions that no longer exist
    const currentTabs = useTabStore.getState().tabs
    Object.values(currentTabs).forEach((tab) => {
      if (tab.sessionName && !sessionNames.has(tab.sessionName)) {
        const ws = useWorkspaceStore.getState().findWorkspaceByTab(tab.id)
        if (ws) useWorkspaceStore.getState().removeTabFromWorkspace(ws.id, tab.id)
        useTabStore.getState().removeTab(tab.id)
      }
    })
  }, [sessions])

  // --- Hash routing: restore tab from URL on mount ---
  useEffect(() => {
    const { tabId } = parseHash()
    if (tabId && useTabStore.getState().tabs[tabId]) {
      setActiveTab(tabId)
    }
  }, [setActiveTab])

  // --- Hash routing: sync activeTabId → URL ---
  useEffect(() => {
    if (activeTabId) setHash(activeTabId)
  }, [activeTabId])

  // --- Hash routing: listen for browser back/forward ---
  useEffect(() => {
    const handler = () => {
      const { tabId } = parseHash()
      if (tabId && useTabStore.getState().tabs[tabId]) setActiveTab(tabId)
    }
    window.addEventListener('hashchange', handler)
    return () => window.removeEventListener('hashchange', handler)
  }, [setActiveTab])

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
      // clearSession handled by session-events handler on handoff 'connected'
      // Update tab type to terminal
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
    removeTab(tabId)
  }, [findWorkspaceByTab, removeTabFromWorkspace, removeTab])

  const handleAddTab = useCallback(() => {
    setSessionPickerOpen(true)
  }, [])

  const handleSessionSelect = useCallback((session: typeof sessions[0]) => {
    setSessionPickerOpen(false)
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
