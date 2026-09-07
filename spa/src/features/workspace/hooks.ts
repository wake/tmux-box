import { useState, useCallback } from 'react'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from './store'
import { createTab } from '../../types/tab'
import { getPrimaryPane, collectLeaves } from '../../lib/pane-tree'
import { renameSession } from '../../lib/host-api'
import { closeTab } from '../../lib/tab-lifecycle'
import type { Tab, PaneContent, PaneRebuildRecord, TerminatedReason } from '../../types/tab'
import type { ContextMenuAction } from '../../components/TabContextMenu'
import type { RebuildEditableField } from '../../components/RebuildActionSet'

/**
 * One `tmux-session` pane the tab-name popover can act on (spec §4.10). A tab
 * may hold several: the popover renders one block per terminal target and
 * every edit is scoped to the target it came from, so a split sibling bound to
 * the same session keeps its own record.
 */
export interface RenameTargetPane {
  tabId: string
  paneId: string
  hostId: string
  sessionCode: string
  tmuxInstance: string
  cachedName: string
  /** Stream panes carry no rebuild record; they only get the legacy rename. */
  mode: 'terminal' | 'stream'
  /** Set when the pane's session is gone — its name row edits the record only. */
  terminated?: TerminatedReason
  record: PaneRebuildRecord
}

/** Every `tmux-session` pane in the tab, in layout order, whatever its mode. */
function collectSessionPanes(tab: Tab): RenameTargetPane[] {
  const targets: RenameTargetPane[] = []
  for (const pane of collectLeaves(tab.layout)) {
    const c = pane.content
    if (c.kind !== 'tmux-session') continue
    targets.push({
      tabId: tab.id,
      paneId: pane.id,
      hostId: c.hostId,
      sessionCode: c.sessionCode,
      tmuxInstance: c.tmuxInstance,
      cachedName: c.cachedName,
      mode: c.mode,
      terminated: c.terminated,
      // A pane that never accumulated a record still has a name and a
      // generation — the same seed shape `applyRebuildPatch` writes against.
      record: c.rebuild ?? { sessionName: c.cachedName, tmuxInstance: c.tmuxInstance, capturedAt: 0 },
    })
  }
  return targets
}

/**
 * The panes the popover renders a rebuild detail block for: terminal panes,
 * dead ones included — a dead pane is exactly the one whose record the user
 * needs to fix. Stream panes are out of scope for the record, as everywhere
 * else in this feature.
 */
export function collectRenameTargets(tab: Tab): RenameTargetPane[] {
  return collectSessionPanes(tab).filter((target) => target.mode === 'terminal')
}

/**
 * The popover's entry condition: any pane it could do something useful with.
 *
 * Wider than {@link collectRenameTargets} in one direction and narrower in
 * another. A stream pane has no rebuild record, but it is still a named tmux
 * session that double-click has always renamed through the legacy single
 * input — gating the entry point on terminal panes alone took that away.
 * A terminated stream pane is left out, matching the pre-feature rule: there
 * is no live session to rename and no record to edit.
 *
 * Looking at the tab's *primary* pane alone (the pre-Task-15 behaviour) left
 * the popover unreachable whenever the first pane happened to be an editor or
 * a dead session, even though another pane in the tab was a good target.
 */
export function collectRenameEntryPanes(tab: Tab): RenameTargetPane[] {
  return collectSessionPanes(tab).filter((target) => target.mode === 'terminal' || !target.terminated)
}

export function useTabWorkspaceActions(displayTabs: Tab[]) {
  const [contextMenu, setContextMenu] = useState<{ tab: Tab; position: { x: number; y: number } } | null>(null)
  const [renameTarget, setRenameTarget] = useState<{ tabId: string; hostId: string; sessionCode: string; tmuxInstance: string; currentName: string; anchorRect: DOMRect } | null>(null)
  const [renameError, setRenameError] = useState<string | undefined>()

  // Tab store
  const tabs = useTabStore((s) => s.tabs)
  const setActiveTab = useTabStore((s) => s.setActiveTab)
  const addTab = useTabStore((s) => s.addTab)
  const reorderTabs = useTabStore((s) => s.reorderTabs)

  // Workspace store
  const activeWorkspaceId = useWorkspaceStore((s) => s.activeWorkspaceId)
  const setActiveWorkspace = useWorkspaceStore((s) => s.setActiveWorkspace)
  const findWorkspaceByTab = useWorkspaceStore((s) => s.findWorkspaceByTab)
  const setWorkspaceActiveTab = useWorkspaceStore((s) => s.setWorkspaceActiveTab)
  const reorderWorkspaceTabs = useWorkspaceStore((s) => s.reorderWorkspaceTabs)

  const handleSelectWorkspace = useCallback((wsId: string) => {
    setActiveWorkspace(wsId)
    const ws = useWorkspaceStore.getState().workspaces.find((w) => w.id === wsId)
    const allTabs = useTabStore.getState().tabs
    if (ws?.activeTabId && allTabs[ws.activeTabId]) setActiveTab(ws.activeTabId)
    else if (ws?.tabs[0]) setActiveTab(ws.tabs[0])
    else setActiveTab(null)
  }, [setActiveWorkspace, setActiveTab])

  const handleSelectTab = useCallback((tabId: string) => {
    setActiveTab(tabId)
    const ws = findWorkspaceByTab(tabId)
    if (ws) {
      setActiveWorkspace(ws.id)
      setWorkspaceActiveTab(ws.id, tabId)
    }

    // markRead is handled by the cross-store subscription in active-session.ts
  }, [setActiveTab, findWorkspaceByTab, setActiveWorkspace, setWorkspaceActiveTab])

  const handleCloseTab = useCallback((tabId: string) => {
    closeTab(tabId)

    // Clear rename popover if the renamed tab was closed
    if (renameTarget?.tabId === tabId) {
      setRenameTarget(null)
      setRenameError(undefined)
    }
  }, [renameTarget])

  const handleAddTab = useCallback(() => {
    const tab = createTab({ kind: 'new-tab' })
    addTab(tab)
    setActiveTab(tab.id)
    useWorkspaceStore.getState().insertTab(tab.id)
  }, [addTab, setActiveTab])

  const handleAddTabToWorkspace = useCallback((wsId: string) => {
    const tab = createTab({ kind: 'new-tab' })
    addTab(tab)
    useWorkspaceStore.getState().insertTab(tab.id, wsId)
    handleSelectTab(tab.id)
  }, [addTab, handleSelectTab])

  const handleReorderWorkspaceTabs = useCallback((wsId: string, tabIds: string[]) => {
    useWorkspaceStore.getState().reorderWorkspaceTabs(wsId, tabIds)
  }, [])

  const handleReorderTabs = useCallback((order: string[]) => {
    if (activeWorkspaceId) {
      reorderWorkspaceTabs(activeWorkspaceId, order)
    } else {
      // Standalone tabs — update global order
      reorderTabs(order)
    }
  }, [reorderTabs, activeWorkspaceId, reorderWorkspaceTabs])

  const handleContextMenu = useCallback((e: React.MouseEvent, tabId: string) => {
    e.preventDefault()
    const tab = tabs[tabId]
    if (tab) setContextMenu({ tab, position: { x: e.clientX, y: e.clientY } })
  }, [tabs])

  const handleMiddleClick = useCallback((tabId: string) => {
    const tab = tabs[tabId]
    if (tab && !tab.locked) handleCloseTab(tabId)
  }, [tabs, handleCloseTab])

  // Opens whenever the tab holds at least one usable tmux pane — not only when
  // the *primary* one is a live session (spec §4.10). The stored target is the
  // pane the legacy single-input confirm path still acts on: the first live
  // session, or the first entry pane at all when every one of them is dead.
  // That path is what a stream-only tab still renames through, since it gets
  // no detail blocks.
  const openRenameForTab = useCallback((tab: Tab, anchorEl?: Element | null) => {
    const targets = collectRenameEntryPanes(tab)
    if (targets.length === 0) return
    const primary = targets.find((target) => !target.terminated) ?? targets[0]
    const el = anchorEl ?? document.querySelector(`[data-tab-id="${tab.id}"]`)
    if (!el) return
    const rect = el.getBoundingClientRect()
    setRenameTarget({
      tabId: tab.id,
      hostId: primary.hostId,
      sessionCode: primary.sessionCode,
      tmuxInstance: primary.tmuxInstance,
      currentName: primary.cachedName || primary.sessionCode,
      anchorRect: rect,
    })
    setRenameError(undefined)
  }, [])

  const handleContextAction = useCallback((action: ContextMenuAction, payload?: string) => {
    if (!contextMenu) return
    const { tab } = contextMenu
    const store = useTabStore.getState()
    const primaryPaneId = getPrimaryPane(tab.layout).id
    switch (action) {
      case 'viewMode-terminal': store.setViewMode(tab.id, primaryPaneId, 'terminal'); break
      case 'viewMode-stream': store.setViewMode(tab.id, primaryPaneId, 'stream'); break
      case 'lock': case 'unlock': store.toggleLock(tab.id); break
      case 'pin': case 'unpin': store.togglePin(tab.id); break
      case 'close': handleCloseTab(tab.id); break
      case 'closeOthers': {
        const displayIds = displayTabs.map((t) => t.id)
        const toClose = displayIds.filter((id) => id !== tab.id && !tabs[id]?.locked)
        toClose.forEach((id) => handleCloseTab(id))
        break
      }
      case 'closeRight': {
        const displayIds = displayTabs.map((t) => t.id)
        const idx = displayIds.indexOf(tab.id)
        if (idx === -1) break
        const toClose = displayIds.slice(idx + 1).filter((id) => !tabs[id]?.locked)
        toClose.forEach((id) => handleCloseTab(id))
        break
      }
      case 'tearOff': {
        if (!window.electronAPI) break
        const tabData = tabs[tab.id]
        if (!tabData) break
        // Must remove tab BEFORE IPC to avoid duplication if locked
        handleCloseTab(tab.id)
        // Only send to new window if tab was actually removed
        if (!useTabStore.getState().tabs[tab.id]) {
          window.electronAPI.tearOffTab(JSON.stringify(tabData))
        }
        break
      }
      case 'rename': {
        openRenameForTab(tab)
        break
      }
      case 'mergeToTab': {
        if (!payload) break
        const sourceTab = tabs[tab.id]
        const targetTab = tabs[payload]
        if (!sourceTab || !targetTab) break
        if (sourceTab.layout.type === 'split') break  // Don't merge multi-pane tabs
        if (sourceTab.locked) break  // Don't merge locked tabs
        if (targetTab.locked) break  // Don't merge into locked tabs
        const sourcePrimary = getPrimaryPane(sourceTab.layout)
        const targetPrimary = getPrimaryPane(targetTab.layout)
        useTabStore.getState().splitPane(payload, targetPrimary.id, 'h', sourcePrimary.content)
        handleCloseTab(tab.id)
        handleSelectTab(payload)  // Focus target tab
        break
      }
    }
  }, [contextMenu, tabs, displayTabs, handleCloseTab, handleSelectTab, openRenameForTab])

  /**
   * The daemon rename. Takes its binding as an argument rather than reading
   * `renameTarget`, because a split tab renders one block per terminal pane and
   * any of them may be the one being renamed (spec §4.10).
   */
  const renameBoundSession = useCallback(async (
    binding: { hostId: string; sessionCode: string; tmuxInstance: string },
    name: string,
  ) => {
    try {
      const res = await renameSession(binding.hostId, binding.sessionCode, name)
      if (!res.ok) {
        const text = await res.text().catch(() => 'Unknown error')
        setRenameError(text)
        return
      }
      // Immediately update tab label (don't wait for WS session event).
      // The name refresh is generation-scoped (spec §4.5), so it needs the
      // generation the rename response was produced under; an older daemon
      // that sends none leaves us with the pane's own recorded instance,
      // which is the binding we just renamed.
      const info = await res.json().catch(() => null) as { tmux_instance?: string } | null
      const tmuxInstance = typeof info?.tmux_instance === 'string' && info.tmux_instance
        ? info.tmux_instance
        : binding.tmuxInstance
      useTabStore.getState().updateSessionCache(binding.hostId, binding.sessionCode, name, tmuxInstance)
      setRenameTarget(null)
      setRenameError(undefined)
    } catch (err) {
      setRenameError(err instanceof Error ? err.message : 'Unknown error')
    }
  }, [])

  const handleRenameConfirm = useCallback(async (name: string) => {
    if (!renameTarget) return
    await renameBoundSession(renameTarget, name)
  }, [renameTarget, renameBoundSession])

  /** A live pane's name row. Never called for a terminated pane (§4.10). */
  const handleRenamePane = useCallback(async (target: RenameTargetPane, name: string) => {
    await renameBoundSession(target, name)
  }, [renameBoundSession])

  /**
   * A record edit from the popover: the pane's name (when its session is gone),
   * its cwd or its resume command. Always pane-scoped, never the session-scoped
   * writer — an edit here must not rewrite a split sibling's record (§4.10) —
   * and nothing is sent to a live session.
   */
  const handleEditRebuildField = useCallback((
    target: RenameTargetPane,
    field: RebuildEditableField,
    value: string,
  ) => {
    useTabStore.getState().setPaneRebuildForPane(
      target.tabId,
      target.paneId,
      { hostId: target.hostId, sessionCode: target.sessionCode, tmuxInstance: target.tmuxInstance },
      { kind: 'field', field, value },
    )
  }, [])

  const handleRenameCancel = useCallback(() => {
    setRenameTarget(null)
    setRenameError(undefined)
  }, [])

  const handleClearRenameError = useCallback(() => {
    setRenameError(undefined)
  }, [])

  const openSingletonAndSelect = useCallback((content: PaneContent, wsId?: string) => {
    const tabId = useTabStore.getState().openSingletonTab(content)
    useWorkspaceStore.getState().insertTab(tabId, wsId)
    handleSelectTab(tabId)
    return tabId
  }, [handleSelectTab])

  // Context menu derived state
  const contextMenuHasRightUnlocked = (() => {
    if (!contextMenu) return false
    const ids = displayTabs.map((t) => t.id)
    const idx = ids.indexOf(contextMenu.tab.id)
    return idx !== -1 && ids.slice(idx + 1).some((id) => !tabs[id]?.locked)
  })()

  return {
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
  }
}
