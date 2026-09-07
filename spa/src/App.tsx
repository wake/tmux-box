// spa/src/App.tsx — v2 重構：wouter Router + Tab/Pane model
import { useCallback, useEffect, useMemo, useState } from 'react'
import { Router } from 'wouter'
import { prefetchWeight } from './features/workspace/lib/icon-path-cache'
import { reorderStandaloneTabOrder } from './features/workspace/lib/reorderStandaloneTabOrder'
import { ActivityBar } from './components/ActivityBar'
import { TabBar } from './components/TabBar'
import { TabContent } from './components/TabContent'
import { StatusBar } from './components/StatusBar'
import { TitleBar } from './components/TitleBar'
import { SidebarRegion } from './components/SidebarRegion'
import { useConfigStore } from './stores/useConfigStore'
import { useTabStore } from './stores/useTabStore'
import { useWorkspaceStore } from './stores/useWorkspaceStore'
import { useHostStore } from './stores/useHostStore'
import { useLayoutStore } from './stores/useLayoutStore'
import { useRelayWsManager } from './hooks/useRelayWsManager'
import { useMultiHostEventWs } from './hooks/useMultiHostEventWs'
import { useRouteSync } from './hooks/useRouteSync'
import { useShortcuts } from './hooks/useShortcuts'
import './lib/browser-shortcuts'
import { useNotificationDispatcher } from './hooks/useNotificationDispatcher'
import { useElectronIpc } from './hooks/useElectronIpc'
import { useDeeplinkResolver } from './hooks/useDeeplinkResolver'
import { useNewTabBootstrap } from './hooks/useNewTabBootstrap'
import { useTabWorkspaceActions } from './hooks/useTabWorkspaceActions'
import { useWorkspaceWindowActions } from './hooks/useWorkspaceWindowActions'
import { isStandaloneTab } from './types/tab'
import {
  getVisibleTabIds,
  nextWorkspaceName,
  WorkspaceContextMenu,
  MigrateTabsDialog,
  WorkspaceEmptyState,
} from './features/workspace'
import { TabContextMenu } from './components/TabContextMenu'
import { RenamePopover } from './components/RenamePopover'
import { ThemeInjector } from './components/ThemeInjector'
import { ErrorBoundary } from './components/ErrorBoundary'
import { getPlatformCapabilities } from './lib/platform'
import type { Tab } from './types/tab'
import { GlobalUndoToast } from './components/GlobalUndoToast'
import { ensureSessionPristine } from './lib/sync/register-sync'
import { inferWorkspaceHostId } from './lib/infer-workspace-host-id'

// Prefetch default icon weight so WorkspaceIcon renders instantly
prefetchWeight('bold').catch(() => {})

export default function App() {
  const isElectron = getPlatformCapabilities().isElectron

  // Host store
  const hostOrder = useHostStore((s) => s.hostOrder)
  const firstHostId = hostOrder[0] ?? ''

  // Existing stores
  const fetchConfig = useConfigStore((s) => s.fetch)

  // Tab store
  const tabs = useTabStore((s) => s.tabs)
  const tabOrder = useTabStore((s) => s.tabOrder)
  const activeTabId = useTabStore((s) => s.activeTabId)

  // Layout store
  const tabPosition = useLayoutStore((s) => s.tabPosition)

  // Workspace store
  const workspaces = useWorkspaceStore((s) => s.workspaces)
  const activeWorkspaceId = useWorkspaceStore((s) => s.activeWorkspaceId)

  // --- Extracted hooks ---
  useRelayWsManager()
  useMultiHostEventWs()
  useRouteSync()
  useShortcuts()
  useNotificationDispatcher()
  // Must precede useElectronIpc: the deeplink resolver has to subscribe before
  // `spa:ready` is sent, or a buffered cold-start deeplink flush is missed.
  useDeeplinkResolver()
  useElectronIpc()
  useNewTabBootstrap()

  // Reconcile workspaceExpanded only when the id set actually changes
  // (workspace rename / tab reorder replace the `workspaces` ref but preserve ids).
  const wsIdsKey = useMemo(() => workspaces.map((w) => w.id).join(','), [workspaces])
  useEffect(() => {
    useLayoutStore.getState().reconcileWorkspaceExpanded(wsIdsKey ? wsIdsKey.split(',') : [])
  }, [wsIdsKey])

  const { handleWsTearOff, handleWsMergeTo } = useWorkspaceWindowActions()

  // --- Derived state ---
  const activeTab = activeTabId ? tabs[activeTabId] : undefined
  const titleText = activeWorkspaceId ? (workspaces.find(w => w.id === activeWorkspaceId)?.name ?? 'Purdex') : 'Purdex'

  // --- Bootstrap: fetch config (sessions fetched by multi-host WS onOpen) ---
  useEffect(() => {
    if (firstHostId) fetchConfig(firstHostId)
  }, [fetchConfig, firstHostId])

  // --- Bootstrap: ensure session-pristine snapshot ---
  useEffect(() => {
    ensureSessionPristine().catch((err) => {
      // Swallow — pristine failures are recoverable (prior pristine stays
      // intact thanks to atomic rotation) and must not become an unhandled
      // rejection that crashes dev mode or pollutes sentry-style reporters.
      console.warn('ensureSessionPristine failed', err)
    })
  }, [])

  // --- Derive visible tabs for display ---
  const visibleTabIds = getVisibleTabIds({
    tabs,
    tabOrder,
    activeTabId,
    workspaces,
    activeWorkspaceId,
  })
  const displayTabs: Tab[] = visibleTabIds.map((id) => tabs[id]).filter(Boolean)

  const standaloneTabIds = useMemo(
    () => tabOrder.filter((id) => isStandaloneTab(id, workspaces)),
    [tabOrder, workspaces],
  )

  const activeStandaloneTabId = activeTabId && isStandaloneTab(activeTabId, workspaces) ? activeTabId : null

  // --- Tab/Workspace action handlers ---
  const {
    contextMenu,
    setContextMenu,
    contextMenuHasRightUnlocked,
    handleSelectWorkspace,
    handleSelectTab,
    handleCloseTab,
    handleAddTab,
    handleAddTabToWorkspace,
    handleReorderWorkspaceTabs,
    handleReorderTabs,
    handleContextMenu,
    handleMiddleClick,
    handleContextAction,
    renameTarget,
    renameError,
    handleRenameConfirm,
    handleRenamePane,
    handleEditRebuildField,
    handleRenameCancel,
    handleClearRenameError,
    openRenameForTab,
    openSingletonAndSelect,
  } = useTabWorkspaceActions(displayTabs)

  const openWsSettings = useCallback((wsId: string) => {
    openSingletonAndSelect({ kind: 'settings', scope: { workspaceId: wsId } }, wsId)
  }, [openSingletonAndSelect])

  // --- Workspace UI state ---
  const [wsContextMenu, setWsContextMenu] = useState<{ wsId: string; position: { x: number; y: number } } | null>(null)
  const [migrateDialog, setMigrateDialog] = useState<{ wsId: string; wsName: string } | null>(null)

  const handleWsContextMenu = useCallback((e: React.MouseEvent, wsId: string) => {
    setWsContextMenu({ wsId, position: { x: e.clientX, y: e.clientY } })
  }, [])

  const handleReorderWorkspaces = useCallback((ids: string[]) => {
    useWorkspaceStore.getState().reorderWorkspaces(ids)
  }, [])

  const handleReorderStandaloneTabs = useCallback((newOrder: string[]) => {
    const current = useTabStore.getState().tabOrder
    useTabStore.getState().reorderTabs(reorderStandaloneTabOrder(current, newOrder))
  }, [])

  const handleCloseTabContextMenu = useCallback(() => {
    setContextMenu(null)
  }, [setContextMenu])

  const handleCloseWsContextMenu = useCallback(() => {
    setWsContextMenu(null)
  }, [])

  const handleSelectHome = useCallback(() => {
    useWorkspaceStore.getState().setActiveWorkspace(null)
    const firstStandalone = standaloneTabIds[0]
    if (firstStandalone) {
      handleSelectTab(firstStandalone)
    } else {
      useTabStore.getState().setActiveTab(null)
    }
  }, [standaloneTabIds, handleSelectTab])

  const handleAddWorkspace = useCallback(() => {
    const names = workspaces.map(w => w.name)
    if (workspaces.length === 0 && tabOrder.length > 0) {
      const ws = useWorkspaceStore.getState().addWorkspace(nextWorkspaceName(names))
      setMigrateDialog({ wsId: ws.id, wsName: ws.name })
    } else {
      const ws = useWorkspaceStore.getState().addWorkspace(nextWorkspaceName(names))
      openWsSettings(ws.id)
    }
  }, [workspaces, tabOrder.length, openWsSettings])

  const handleOpenHosts = useCallback(() => {
    openSingletonAndSelect({ kind: 'hosts' })
  }, [openSingletonAndSelect])

  const handleOpenSettings = useCallback(() => {
    openSingletonAndSelect({ kind: 'settings', scope: 'global' })
  }, [openSingletonAndSelect])

  const handleViewModeChange = useCallback((tabId: string, paneId: string, mode: 'terminal' | 'stream') => {
    useTabStore.getState().setViewMode(tabId, paneId, mode)
  }, [])

  const handleNavigateToHost = useCallback((hostId: string) => {
    openSingletonAndSelect({ kind: 'hosts' })
    useHostStore.getState().setActiveHost(hostId)
  }, [openSingletonAndSelect])

  const handleRenameTab = useCallback((tabId: string) => {
    const tab = tabs[tabId]
    if (tab) openRenameForTab(tab)
  }, [tabs, openRenameForTab])

  const handleMigrateConfirm = useCallback(() => {
    if (!migrateDialog) return
    tabOrder.forEach((tabId) => {
      useWorkspaceStore.getState().insertTab(tabId, migrateDialog.wsId)
    })
    setMigrateDialog(null)
    openWsSettings(migrateDialog.wsId)
  }, [migrateDialog, tabOrder, openWsSettings])

  const handleMigrateSkip = useCallback(() => {
    setMigrateDialog(null)
    useWorkspaceStore.getState().setActiveWorkspace(null)
  }, [])

  return (
    <ErrorBoundary>
    <Router>
      <ThemeInjector />
      <div className="h-screen flex flex-col bg-surface-primary text-text-primary">
        {isElectron && <TitleBar title={titleText} />}
        <div className="flex-1 flex min-h-0">
          <ActivityBar
            workspaces={workspaces}
            activeWorkspaceId={activeStandaloneTabId ? null : activeWorkspaceId}
            activeStandaloneTabId={activeStandaloneTabId}
            onSelectWorkspace={handleSelectWorkspace}
            onSelectHome={handleSelectHome}
            standaloneTabIds={standaloneTabIds}
            onAddWorkspace={handleAddWorkspace}
            onReorderWorkspaces={handleReorderWorkspaces}
            onContextMenuWorkspace={handleWsContextMenu}
            onOpenHosts={handleOpenHosts}
            onOpenSettings={handleOpenSettings}
            // Phase 2
            tabsById={tabs}
            activeTabId={activeTabId}
            onSelectTab={handleSelectTab}
            onCloseTab={handleCloseTab}
            onMiddleClickTab={handleMiddleClick}
            onContextMenuTab={handleContextMenu}
            onRenameTab={handleRenameTab}
            onReorderWorkspaceTabs={handleReorderWorkspaceTabs}
            onReorderStandaloneTabs={handleReorderStandaloneTabs}
            onAddTabToWorkspace={handleAddTabToWorkspace}
          />
          <SidebarRegion region="primary-sidebar" resizeEdge="right" />
          <div className="flex-1 flex flex-col min-w-0">
            {tabPosition !== 'left' && (
              <TabBar
                tabs={displayTabs}
                activeTabId={activeTabId}
                onSelectTab={handleSelectTab}
                onCloseTab={handleCloseTab}
                onAddTab={handleAddTab}
                onReorderTabs={handleReorderTabs}
                onMiddleClick={handleMiddleClick}
                onContextMenu={handleContextMenu}
                onRenameTab={handleRenameTab}
              />
            )}
            <div className="flex-1 flex overflow-hidden">
              <SidebarRegion region="primary-panel" resizeEdge="right" />
              {visibleTabIds.length === 0 && activeWorkspaceId !== null ? (
                <WorkspaceEmptyState />
              ) : (
                <TabContent
                  activeTab={activeTab ?? null}
                  allTabs={tabOrder.map((id) => tabs[id]).filter(Boolean)}
                />
              )}
              <SidebarRegion region="secondary-panel" resizeEdge="left" />
            </div>
            <StatusBar
              activeTab={activeTab ?? null}
              onViewModeChange={handleViewModeChange}
              onNavigateToHost={handleNavigateToHost}
              onStartRename={openRenameForTab}
            />
          </div>
          <SidebarRegion region="secondary-sidebar" resizeEdge="left" />
        {contextMenu && (
          <TabContextMenu
            tab={contextMenu.tab}
            position={contextMenu.position}
            onClose={handleCloseTabContextMenu}
            onAction={handleContextAction}
            hasOtherUnlocked={displayTabs.some((t) => t.id !== contextMenu.tab.id && !t.locked)}
            hasRightUnlocked={contextMenuHasRightUnlocked}
            targetTabs={displayTabs.filter((t) =>
              t.id !== contextMenu.tab.id && t.layout.type === 'split' && !t.locked
            )}
          />
        )}
        {renameTarget && (
          <RenamePopover
            anchorRect={renameTarget.anchorRect}
            currentName={renameTarget.currentName}
            onConfirm={handleRenameConfirm}
            onCancel={handleRenameCancel}
            error={renameError}
            onClearError={handleClearRenameError}
            tab={tabs[renameTarget.tabId]}
            onRenamePane={handleRenamePane}
            onEditRebuildField={handleEditRebuildField}
          />
        )}
        {wsContextMenu && (() => {
          const ws = workspaces.find((w) => w.id === wsContextMenu.wsId)
          if (!ws) return null
          // Spec v4 §3.2.1 — workspace hostId via majority vote, never silently
          // fall back to useHostStore.activeHostId.
          const hostId = inferWorkspaceHostId(ws, tabs)
          return (
            <WorkspaceContextMenu
              position={wsContextMenu.position}
              workspaceId={wsContextMenu.wsId}
              hostId={hostId}
              onSettings={() => openWsSettings(wsContextMenu.wsId)}
              onTearOff={window.electronAPI ? () => handleWsTearOff(wsContextMenu.wsId) : undefined}
              onMergeTo={window.electronAPI ? (targetWindowId) => handleWsMergeTo(wsContextMenu.wsId, targetWindowId) : undefined}
              onClose={handleCloseWsContextMenu}
            />
          )
        })()}
        {migrateDialog && (
          <MigrateTabsDialog
            tabCount={tabOrder.length}
            workspaceName={migrateDialog.wsName}
            onMigrate={handleMigrateConfirm}
            onSkip={handleMigrateSkip}
          />
        )}
        </div>
      </div>
      <GlobalUndoToast />
    </Router>
    </ErrorBoundary>
  )
}
