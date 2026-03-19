import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { createWorkspace, type Workspace } from '../types/tab'

interface WorkspaceState {
  workspaces: Workspace[]
  activeWorkspaceId: string

  addWorkspace: (name: string, opts?: { color?: string; icon?: string }) => Workspace
  removeWorkspace: (wsId: string) => void
  setActiveWorkspace: (wsId: string) => void
  addTabToWorkspace: (wsId: string, tabId: string) => void
  removeTabFromWorkspace: (wsId: string, tabId: string) => void
  setWorkspaceActiveTab: (wsId: string, tabId: string) => void
  findWorkspaceByTab: (tabId: string) => Workspace | null
  reset: () => void
}

function createDefaultState() {
  const defaultWs = createWorkspace({ name: 'Default', color: '#7a6aaa' })
  return { workspaces: [defaultWs], activeWorkspaceId: defaultWs.id }
}

export const useWorkspaceStore = create<WorkspaceState>()(
  persist(
    (set, get) => ({
      ...createDefaultState(),

      addWorkspace: (name, opts) => {
        const ws = createWorkspace({ name, ...opts })
        set((state) => ({ workspaces: [...state.workspaces, ws] }))
        return ws
      },

      removeWorkspace: (wsId) =>
        set((state) => {
          if (state.workspaces.length <= 1) return state
          const remaining = state.workspaces.filter((ws) => ws.id !== wsId)
          const activeId = state.activeWorkspaceId === wsId ? remaining[0].id : state.activeWorkspaceId
          return { workspaces: remaining, activeWorkspaceId: activeId }
        }),

      setActiveWorkspace: (wsId) =>
        set({ activeWorkspaceId: wsId }),

      addTabToWorkspace: (wsId, tabId) =>
        set((state) => ({
          workspaces: state.workspaces.map((ws) => {
            if (ws.id !== wsId) return ws
            if (ws.tabs.includes(tabId)) return ws
            return { ...ws, tabs: [...ws.tabs, tabId] }
          }),
        })),

      removeTabFromWorkspace: (wsId, tabId) =>
        set((state) => ({
          workspaces: state.workspaces.map((ws) =>
            ws.id === wsId
              ? {
                  ...ws,
                  tabs: ws.tabs.filter((id) => id !== tabId),
                  activeTabId: ws.activeTabId === tabId ? null : ws.activeTabId,
                }
              : ws,
          ),
        })),

      setWorkspaceActiveTab: (wsId, tabId) =>
        set((state) => ({
          workspaces: state.workspaces.map((ws) =>
            ws.id === wsId ? { ...ws, activeTabId: tabId } : ws,
          ),
        })),

      findWorkspaceByTab: (tabId) => {
        return get().workspaces.find((ws) => ws.tabs.includes(tabId)) ?? null
      },

      reset: () => set(createDefaultState()),
    }),
    {
      name: 'tbox-workspaces',
      partialize: (state) => ({
        workspaces: state.workspaces,
        activeWorkspaceId: state.activeWorkspaceId,
      }),
    },
  ),
)
