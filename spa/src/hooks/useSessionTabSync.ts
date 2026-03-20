import { useEffect } from 'react'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { createTab } from '../types/tab'
import type { Session } from '../lib/api'

export function useSessionTabSync(sessions: Session[]) {
  useEffect(() => {
    const sessionNames = new Set(sessions.map((s) => s.name))

    // Add tabs for new sessions
    sessions.forEach((s) => {
      const currentTabs = useTabStore.getState().tabs
      const existingTab = Object.values(currentTabs).find((t) => t.sessionName === s.name)
      if (!existingTab && !useTabStore.getState().isSessionDismissed(s.name)) {
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
    // Skip cleanup when sessions is empty (initial render before fetch resolves)
    if (sessions.length > 0) {
      const currentTabs = useTabStore.getState().tabs
      Object.values(currentTabs).forEach((tab) => {
        if (tab.sessionName && !sessionNames.has(tab.sessionName)) {
          const ws = useWorkspaceStore.getState().findWorkspaceByTab(tab.id)
          if (ws) useWorkspaceStore.getState().removeTabFromWorkspace(ws.id, tab.id)
          useTabStore.getState().removeTab(tab.id)
        }
      })
    }
  }, [sessions])
}
