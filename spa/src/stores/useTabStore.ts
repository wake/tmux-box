import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import type { Tab } from '../types/tab'

interface TabState {
  tabs: Record<string, Tab>
  tabOrder: string[]
  activeTabId: string | null
  dismissedSessions: string[]

  addTab: (tab: Tab) => void
  removeTab: (tabId: string) => void
  dismissTab: (tabId: string) => void
  undismissSession: (sessionName: string) => void
  isSessionDismissed: (sessionName: string) => boolean
  setActiveTab: (tabId: string) => void
  reorderTabs: (order: string[]) => void
  updateTab: (tabId: string, updates: Partial<Tab>) => void
  getActiveTab: () => Tab | null
}

export const useTabStore = create<TabState>()(
  persist(
    (set, get) => ({
      tabs: {},
      tabOrder: [],
      activeTabId: null,
      dismissedSessions: [],

      addTab: (tab) =>
        set((state) => ({
          tabs: { ...state.tabs, [tab.id]: tab },
          tabOrder: [...state.tabOrder, tab.id],
          activeTabId: state.activeTabId ?? tab.id,
        })),

      removeTab: (tabId) =>
        set((state) => {
          if (!state.tabs[tabId]) return state
          // eslint-disable-next-line @typescript-eslint/no-unused-vars
          const { [tabId]: _removed, ...remainingTabs } = state.tabs
          const newOrder = state.tabOrder.filter((id) => id !== tabId)
          let newActiveId = state.activeTabId
          if (state.activeTabId === tabId) {
            const oldIndex = state.tabOrder.indexOf(tabId)
            newActiveId = newOrder[Math.min(oldIndex, newOrder.length - 1)] ?? null
          }
          return { tabs: remainingTabs, tabOrder: newOrder, activeTabId: newActiveId }
        }),

      dismissTab: (tabId) =>
        set((state) => {
          const tab = state.tabs[tabId]
          if (!tab) return state
          // eslint-disable-next-line @typescript-eslint/no-unused-vars
          const { [tabId]: _removed, ...remainingTabs } = state.tabs
          const newOrder = state.tabOrder.filter((id) => id !== tabId)
          let newActiveId = state.activeTabId
          if (state.activeTabId === tabId) {
            const oldIndex = state.tabOrder.indexOf(tabId)
            newActiveId = newOrder[Math.min(oldIndex, newOrder.length - 1)] ?? null
          }
          const dismissed = tab.sessionName
            ? [...state.dismissedSessions, tab.sessionName]
            : state.dismissedSessions
          return { tabs: remainingTabs, tabOrder: newOrder, activeTabId: newActiveId, dismissedSessions: dismissed }
        }),

      undismissSession: (sessionName) =>
        set((state) => ({
          dismissedSessions: state.dismissedSessions.filter((s) => s !== sessionName),
        })),

      isSessionDismissed: (sessionName) => {
        return get().dismissedSessions.includes(sessionName)
      },

      setActiveTab: (tabId) =>
        set((state) => {
          if (!state.tabs[tabId]) return state
          return { activeTabId: tabId }
        }),

      reorderTabs: (order) =>
        set({ tabOrder: order }),

      updateTab: (tabId, updates) =>
        set((state) => {
          if (!state.tabs[tabId]) return state
          return { tabs: { ...state.tabs, [tabId]: { ...state.tabs[tabId], ...updates } } }
        }),

      getActiveTab: () => {
        const { tabs, activeTabId } = get()
        return activeTabId ? tabs[activeTabId] ?? null : null
      },
    }),
    {
      name: 'tbox-tabs',
      partialize: (state) => ({
        tabs: state.tabs,
        tabOrder: state.tabOrder,
        activeTabId: state.activeTabId,
        dismissedSessions: state.dismissedSessions,
      }),
    },
  ),
)
