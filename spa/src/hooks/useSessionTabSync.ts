import { useEffect, useState } from 'react'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { createSessionTab } from '../types/tab'
import { getSessionName } from '../lib/tab-helpers'
import type { Session } from '../lib/api'

export function useSessionTabSync(sessions: Session[]) {
  // Wait for Zustand persist hydration before syncing tabs
  const [hydrated, setHydrated] = useState(() => useTabStore.persist.hasHydrated())

  useEffect(() => {
    if (hydrated) return
    const unsub = useTabStore.persist.onFinishHydration(() => {
      setHydrated(true)
    })
    return unsub
  }, [hydrated])

  useEffect(() => {
    if (!hydrated) return

    const sessionNames = new Set(sessions.map((s) => s.name))

    // Add tabs for new sessions
    sessions.forEach((s) => {
      const currentTabs = useTabStore.getState().tabs
      const existingTab = Object.values(currentTabs).find((t) => getSessionName(t) === s.name)
      if (!existingTab && !useTabStore.getState().isSessionDismissed(s.name)) {
        const tab = createSessionTab({
          label: s.name,
          hostId: 'local',
          sessionName: s.name,
          sessionCode: s.code,
          viewMode: s.mode === 'stream' ? 'stream' : 'terminal',
        })
        useTabStore.getState().addTab(tab)
        const defaultWsId = useWorkspaceStore.getState().workspaces[0]?.id
        if (defaultWsId) useWorkspaceStore.getState().addTabToWorkspace(defaultWsId, tab.id)
      }
    })

    // Remove tabs for sessions that no longer exist
    if (sessions.length > 0) {
      const currentTabs = useTabStore.getState().tabs
      Object.values(currentTabs).forEach((tab) => {
        const sName = getSessionName(tab)
        if (sName && !sessionNames.has(sName)) {
          const ws = useWorkspaceStore.getState().findWorkspaceByTab(tab.id)
          if (ws) useWorkspaceStore.getState().removeTabFromWorkspace(ws.id, tab.id)
          useTabStore.getState().removeTab(tab.id)
        }
      })
    }
  }, [sessions, hydrated])
}
