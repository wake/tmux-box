import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import type { Tab } from '../types/tab'
import { getSessionName } from '../lib/tab-helpers'

export interface DismissedSession {
  sessionName: string
  pinned: boolean
}

interface TabState {
  tabs: Record<string, Tab>
  tabOrder: string[]
  activeTabId: string | null
  dismissedSessions: DismissedSession[]

  addTab: (tab: Tab) => void
  removeTab: (tabId: string) => void
  dismissTab: (tabId: string) => void
  undismissSession: (sessionName: string) => void
  isSessionDismissed: (sessionName: string) => boolean
  setActiveTab: (tabId: string) => void
  reorderTabs: (order: string[]) => void
  updateTab: (tabId: string, updates: Partial<Tab>) => void
  setViewMode: (tabId: string, viewMode: string) => void
  getActiveTab: () => Tab | null
  pinTab: (tabId: string) => void
  unpinTab: (tabId: string) => void
  lockTab: (tabId: string) => void
  unlockTab: (tabId: string) => void
}

export function migrateTabStore(persisted: any, version: number) {
  if (version < 1) {
    // v0→v1: type union → open type + data bag
    const tabs: Record<string, any> = persisted.tabs ?? {}
    const migrated: Record<string, any> = {}
    for (const [id, tab] of Object.entries(tabs) as [string, any][]) {
      if (tab.type === 'terminal' || tab.type === 'stream') {
        const { sessionName, ...rest } = tab
        migrated[id] = {
          ...rest,
          type: 'session',
          viewMode: tab.type === 'stream' ? 'stream' : 'terminal',
          data: { sessionName },
        }
      } else if (tab.type === 'editor') {
        const { filePath, isDirty, ...rest } = tab
        migrated[id] = {
          ...rest,
          type: 'editor',
          data: { filePath, isDirty: isDirty ?? false },
        }
      } else {
        migrated[id] = { ...tab, data: tab.data ?? {} }
      }
    }
    persisted = { ...persisted, tabs: migrated }
  }
  if (version < 2) {
    // v1→v2: add pinned/locked defaults
    const tabs: Record<string, any> = persisted.tabs ?? {}
    const migrated: Record<string, any> = {}
    for (const [id, tab] of Object.entries(tabs) as [string, any][]) {
      migrated[id] = { ...tab, pinned: tab.pinned ?? false, locked: tab.locked ?? false }
    }
    persisted = { ...persisted, tabs: migrated }
  }
  if (version < 3) {
    // v2→v3: dismissedSessions string[] → { sessionName, pinned }[]
    persisted.dismissedSessions = (persisted.dismissedSessions ?? [])
      .map((s: any) => typeof s === 'string' ? { sessionName: s, pinned: false } : s)
  }
  return persisted
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
          if (state.tabs[tabId].locked) return state
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
          if (tab.locked) return state
          // eslint-disable-next-line @typescript-eslint/no-unused-vars
          const { [tabId]: _removed, ...remainingTabs } = state.tabs
          const newOrder = state.tabOrder.filter((id) => id !== tabId)
          let newActiveId = state.activeTabId
          if (state.activeTabId === tabId) {
            const oldIndex = state.tabOrder.indexOf(tabId)
            newActiveId = newOrder[Math.min(oldIndex, newOrder.length - 1)] ?? null
          }
          const sessionName = getSessionName(tab)
          const dismissed = sessionName
            ? [...state.dismissedSessions, { sessionName, pinned: tab.pinned }]
            : state.dismissedSessions
          return { tabs: remainingTabs, tabOrder: newOrder, activeTabId: newActiveId, dismissedSessions: dismissed }
        }),

      undismissSession: (sessionName) =>
        set((state) => ({
          dismissedSessions: state.dismissedSessions.filter((s) => s.sessionName !== sessionName),
        })),

      isSessionDismissed: (sessionName) => {
        return get().dismissedSessions.some((s) => s.sessionName === sessionName)
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
          // Strip pinned/locked to protect invariants — use pinTab/lockTab instead
          const { pinned: _p, locked: _l, ...safeUpdates } = updates as any
          return { tabs: { ...state.tabs, [tabId]: { ...state.tabs[tabId], ...safeUpdates } } }
        }),

      setViewMode: (tabId, viewMode) =>
        set((state) => {
          if (!state.tabs[tabId]) return state
          return { tabs: { ...state.tabs, [tabId]: { ...state.tabs[tabId], viewMode } } }
        }),

      getActiveTab: () => {
        const { tabs, activeTabId } = get()
        return activeTabId ? tabs[activeTabId] ?? null : null
      },

      pinTab: (tabId) =>
        set((state) => {
          const tab = state.tabs[tabId]
          if (!tab || tab.pinned) return state
          const updated = { ...tab, pinned: true }
          const newOrder = state.tabOrder.filter((id) => id !== tabId)
          const firstNormalIdx = newOrder.findIndex((id) => !state.tabs[id]?.pinned)
          const insertIdx = firstNormalIdx === -1 ? newOrder.length : firstNormalIdx
          newOrder.splice(insertIdx, 0, tabId)
          return { tabs: { ...state.tabs, [tabId]: updated }, tabOrder: newOrder }
        }),

      unpinTab: (tabId) =>
        set((state) => {
          const tab = state.tabs[tabId]
          if (!tab || !tab.pinned) return state
          const updated = { ...tab, pinned: false }
          const newOrder = state.tabOrder.filter((id) => id !== tabId)
          const firstNormalIdx = newOrder.findIndex((id) => !state.tabs[id]?.pinned)
          const insertIdx = firstNormalIdx === -1 ? newOrder.length : firstNormalIdx
          newOrder.splice(insertIdx, 0, tabId)
          return { tabs: { ...state.tabs, [tabId]: updated }, tabOrder: newOrder }
        }),

      lockTab: (tabId) =>
        set((state) => {
          if (!state.tabs[tabId]) return state
          return { tabs: { ...state.tabs, [tabId]: { ...state.tabs[tabId], locked: true } } }
        }),

      unlockTab: (tabId) =>
        set((state) => {
          const tab = state.tabs[tabId]
          if (!tab) return state
          return { tabs: { ...state.tabs, [tabId]: { ...tab, locked: false } } }
        }),
    }),
    {
      name: 'tbox-tabs',
      version: 3,
      migrate: migrateTabStore,
      partialize: (state) => ({
        tabs: state.tabs,
        tabOrder: state.tabOrder,
        activeTabId: state.activeTabId,
        dismissedSessions: state.dismissedSessions,
      }),
    },
  ),
)
