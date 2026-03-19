# Phase 1: 分頁系統 + Activity Bar 基礎 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 將 tmux-box SPA 從「單 session 檢視」升級為「多分頁 + Activity Bar」架構，保持所有現有功能正常運作。

**Architecture:** 在現有 SessionPanel + TopBar + Content 佈局之上，加入 Tab 層——每個「session + mode」成為一個 Tab。Activity Bar 垂直排列在最左側作為工作區切換器（Phase 1 只有一個預設工作區）。TabBar 水平顯示分頁。現有的 TerminalView/ConversationView 由 TabContent 根據 tab type 動態渲染。Hash routing 從 `#/{uid}/{mode}` 升級為 `#/tab/{tabId}`。

**Tech Stack:** React 19, Zustand 5, TypeScript 5.9, Tailwind 4, Vitest, Phosphor Icons

**Spec:** `docs/superpowers/specs/2026-03-20-tabbed-workspace-ui-design.md`

---

## File Structure

### New Files

| File | Responsibility |
|------|---------------|
| `spa/src/types/tab.ts` | Tab, Workspace, SidebarZone 型別定義 |
| `spa/src/stores/useTabStore.ts` | Tab CRUD、排序、活躍分頁切換、持久化 |
| `spa/src/stores/useWorkspaceStore.ts` | Workspace 定義、tabs 歸屬、Activity Bar 排序 |
| `spa/src/components/ActivityBar.tsx` | 垂直圖示列：工作區切換 + 獨立分頁 |
| `spa/src/components/TabBar.tsx` | 水平分頁列：分頁切換 + 關閉 + 新增 |
| `spa/src/components/TabContent.tsx` | 根據 tab type 動態渲染 Terminal/Stream/Editor |
| `spa/src/components/StatusBar.tsx` | 底部狀態列 |
| `spa/src/hooks/useIsMobile.ts` | 響應式 breakpoint 偵測 |

### Modified Files

| File | Changes |
|------|---------|
| `spa/src/App.tsx` | 重構佈局：ActivityBar + TabBar + TabContent + StatusBar |
| `spa/src/components/TerminalView.tsx` | 無改動，TabContent 直接傳入相同 props |
| `spa/src/components/ConversationView.tsx` | 無改動，TabContent 直接傳入相同 props |
| `spa/src/components/SessionPanel.tsx` | Phase 1 從佈局移除（Phase 3 重新引入為側欄面板） |
| `spa/src/components/TopBar.tsx` | Phase 1 移除，功能被 TabBar 取代 |
| `spa/src/index.css` | 新增 Activity Bar 相關基礎樣式 |

### Test Files

| File | Tests |
|------|-------|
| `spa/src/types/tab.test.ts` | 型別 helper functions |
| `spa/src/stores/useTabStore.test.ts` | Tab CRUD、切換、持久化 |
| `spa/src/stores/useWorkspaceStore.test.ts` | Workspace 管理、tab 歸屬 |
| `spa/src/components/ActivityBar.test.tsx` | 渲染、點擊切換 |
| `spa/src/components/TabBar.test.tsx` | 渲染、切換、關閉、新增 |
| `spa/src/components/TabContent.test.tsx` | 根據 type 渲染正確元件 |
| `spa/src/components/StatusBar.test.tsx` | 顯示連線資訊 |
| `spa/src/hooks/useIsMobile.test.ts` | breakpoint 判斷 |

---

## Task 1: Tab 型別定義

**Files:**
- Create: `spa/src/types/tab.ts`
- Test: `spa/src/types/tab.test.ts`

- [ ] **Step 1: Write failing test for Tab type helpers**

```typescript
// spa/src/types/tab.test.ts
import { describe, it, expect } from 'vitest'
import { createTab, createWorkspace, isStandaloneTab } from './tab'

describe('createTab', () => {
  it('creates a terminal tab with generated id', () => {
    const tab = createTab({ type: 'terminal', label: 'dev-server', hostId: 'mlab', sessionName: 'dev-server' })
    expect(tab.id).toBeTruthy()
    expect(tab.type).toBe('terminal')
    expect(tab.label).toBe('dev-server')
    expect(tab.hostId).toBe('mlab')
    expect(tab.sessionName).toBe('dev-server')
  })

  it('creates a stream tab', () => {
    const tab = createTab({ type: 'stream', label: 'claude-code', hostId: 'mlab', sessionName: 'claude-code' })
    expect(tab.type).toBe('stream')
  })

  it('creates an editor tab', () => {
    const tab = createTab({ type: 'editor', label: 'App.tsx', hostId: 'mlab', filePath: '/src/App.tsx' })
    expect(tab.type).toBe('editor')
    expect(tab.filePath).toBe('/src/App.tsx')
    expect(tab.isDirty).toBe(false)
  })
})

describe('createWorkspace', () => {
  it('creates a workspace with defaults', () => {
    const ws = createWorkspace({ name: 'My Project' })
    expect(ws.id).toBeTruthy()
    expect(ws.name).toBe('My Project')
    expect(ws.color).toBeTruthy()
    expect(ws.tabs).toEqual([])
    expect(ws.directories).toEqual([])
  })
})

describe('isStandaloneTab', () => {
  it('returns true when tab is not in any workspace', () => {
    const tab = createTab({ type: 'terminal', label: 'misc', hostId: 'mlab' })
    const workspaces = [createWorkspace({ name: 'WS1' })]
    expect(isStandaloneTab(tab.id, workspaces)).toBe(true)
  })

  it('returns false when tab is in a workspace', () => {
    const tab = createTab({ type: 'terminal', label: 'dev', hostId: 'mlab' })
    const ws = createWorkspace({ name: 'WS1' })
    ws.tabs = [tab.id]
    expect(isStandaloneTab(tab.id, [ws])).toBe(false)
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/types/tab.test.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Implement types and helpers**

```typescript
// spa/src/types/tab.ts

export interface Tab {
  id: string
  type: 'terminal' | 'stream' | 'editor'
  label: string
  icon: string
  hostId: string
  sessionName?: string
  filePath?: string
  isDirty?: boolean
}

export interface Workspace {
  id: string
  name: string
  color: string
  icon?: string
  directories: PinnedItem[]
  tabs: string[]        // tab IDs (ordered)
  activeTabId: string | null
  sidebarState: WorkspaceSidebarState
}

export interface PinnedItem {
  type: 'directory' | 'file'
  hostId: string
  path: string
}

export type SidebarZone = 'left-outer' | 'left-inner' | 'right-inner' | 'right-outer'

export interface WorkspaceSidebarState {
  zones: Record<SidebarZone, {
    activePanelId?: string
    width: number
    mode: 'fixed' | 'default' | 'collapsed'
  }>
}

const WORKSPACE_COLORS = ['#7a6aaa', '#6aaa7a', '#aa6a7a', '#6a8aaa', '#aa8a6a', '#8a6aaa']

function generateId(): string {
  return crypto.randomUUID()
}

function iconForType(type: Tab['type']): string {
  switch (type) {
    case 'terminal': return 'Terminal'
    case 'stream': return 'ChatCircleDots'
    case 'editor': return 'File'
  }
}

function defaultSidebarState(): WorkspaceSidebarState {
  const defaultZone = { width: 200, mode: 'default' as const }
  return {
    zones: {
      'left-outer': { ...defaultZone },
      'left-inner': { ...defaultZone },
      'right-inner': { ...defaultZone },
      'right-outer': { ...defaultZone },
    },
  }
}

export function createTab(opts: Omit<Tab, 'id' | 'icon'> & { icon?: string }): Tab {
  return {
    id: generateId(),
    icon: opts.icon ?? iconForType(opts.type),
    isDirty: opts.type === 'editor' ? false : undefined,
    ...opts,
  }
}

export function createWorkspace(opts: { name: string; color?: string; icon?: string }): Workspace {
  return {
    id: generateId(),
    name: opts.name,
    color: opts.color ?? WORKSPACE_COLORS[Math.floor(Math.random() * WORKSPACE_COLORS.length)],
    icon: opts.icon,
    directories: [],
    tabs: [],
    activeTabId: null,
    sidebarState: defaultSidebarState(),
  }
}

export function isStandaloneTab(tabId: string, workspaces: Workspace[]): boolean {
  return !workspaces.some(ws => ws.tabs.includes(tabId))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/types/tab.test.ts`
Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
git add spa/src/types/tab.ts spa/src/types/tab.test.ts
git commit -m "feat: add Tab and Workspace type definitions with helpers"
```

---

## Task 2: useTabStore

**Files:**
- Create: `spa/src/stores/useTabStore.ts`
- Test: `spa/src/stores/useTabStore.test.ts`

- [ ] **Step 1: Write failing tests**

```typescript
// spa/src/stores/useTabStore.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { useTabStore } from './useTabStore'
import { createTab } from '../types/tab'

describe('useTabStore', () => {
  beforeEach(() => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  })

  it('adds a tab', () => {
    const tab = createTab({ type: 'terminal', label: 'dev', hostId: 'mlab' })
    useTabStore.getState().addTab(tab)

    const state = useTabStore.getState()
    expect(state.tabs[tab.id]).toEqual(tab)
    expect(state.tabOrder).toContain(tab.id)
  })

  it('sets active tab on add if none active', () => {
    const tab = createTab({ type: 'terminal', label: 'dev', hostId: 'mlab' })
    useTabStore.getState().addTab(tab)
    expect(useTabStore.getState().activeTabId).toBe(tab.id)
  })

  it('does not change active tab when adding second tab', () => {
    const tab1 = createTab({ type: 'terminal', label: 'dev', hostId: 'mlab' })
    const tab2 = createTab({ type: 'stream', label: 'claude', hostId: 'mlab' })
    useTabStore.getState().addTab(tab1)
    useTabStore.getState().addTab(tab2)
    expect(useTabStore.getState().activeTabId).toBe(tab1.id)
  })

  it('removes a tab', () => {
    const tab = createTab({ type: 'terminal', label: 'dev', hostId: 'mlab' })
    useTabStore.getState().addTab(tab)
    useTabStore.getState().removeTab(tab.id)

    expect(useTabStore.getState().tabs[tab.id]).toBeUndefined()
    expect(useTabStore.getState().tabOrder).not.toContain(tab.id)
  })

  it('activates next tab when removing active tab', () => {
    const tab1 = createTab({ type: 'terminal', label: 'dev', hostId: 'mlab' })
    const tab2 = createTab({ type: 'stream', label: 'claude', hostId: 'mlab' })
    useTabStore.getState().addTab(tab1)
    useTabStore.getState().addTab(tab2)
    useTabStore.getState().setActiveTab(tab1.id)
    useTabStore.getState().removeTab(tab1.id)

    expect(useTabStore.getState().activeTabId).toBe(tab2.id)
  })

  it('sets activeTabId to null when removing last tab', () => {
    const tab = createTab({ type: 'terminal', label: 'dev', hostId: 'mlab' })
    useTabStore.getState().addTab(tab)
    useTabStore.getState().removeTab(tab.id)
    expect(useTabStore.getState().activeTabId).toBeNull()
  })

  it('switches active tab', () => {
    const tab1 = createTab({ type: 'terminal', label: 'dev', hostId: 'mlab' })
    const tab2 = createTab({ type: 'stream', label: 'claude', hostId: 'mlab' })
    useTabStore.getState().addTab(tab1)
    useTabStore.getState().addTab(tab2)
    useTabStore.getState().setActiveTab(tab2.id)

    expect(useTabStore.getState().activeTabId).toBe(tab2.id)
  })

  it('reorders tabs', () => {
    const tab1 = createTab({ type: 'terminal', label: 'a', hostId: 'mlab' })
    const tab2 = createTab({ type: 'terminal', label: 'b', hostId: 'mlab' })
    const tab3 = createTab({ type: 'terminal', label: 'c', hostId: 'mlab' })
    useTabStore.getState().addTab(tab1)
    useTabStore.getState().addTab(tab2)
    useTabStore.getState().addTab(tab3)
    useTabStore.getState().reorderTabs([tab3.id, tab1.id, tab2.id])

    expect(useTabStore.getState().tabOrder).toEqual([tab3.id, tab1.id, tab2.id])
  })

  it('updates tab properties', () => {
    const tab = createTab({ type: 'editor', label: 'file.ts', hostId: 'mlab', filePath: '/file.ts' })
    useTabStore.getState().addTab(tab)
    useTabStore.getState().updateTab(tab.id, { isDirty: true, label: 'file.ts *' })

    expect(useTabStore.getState().tabs[tab.id].isDirty).toBe(true)
    expect(useTabStore.getState().tabs[tab.id].label).toBe('file.ts *')
  })

  it('returns active tab via getActiveTab', () => {
    const tab = createTab({ type: 'terminal', label: 'dev', hostId: 'mlab' })
    useTabStore.getState().addTab(tab)
    expect(useTabStore.getState().getActiveTab()).toEqual(tab)
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/stores/useTabStore.test.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Implement useTabStore**

```typescript
// spa/src/stores/useTabStore.ts
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import type { Tab } from '../types/tab'

interface TabState {
  tabs: Record<string, Tab>
  tabOrder: string[]
  activeTabId: string | null

  addTab: (tab: Tab) => void
  removeTab: (tabId: string) => void
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

      addTab: (tab) =>
        set((state) => ({
          tabs: { ...state.tabs, [tab.id]: tab },
          tabOrder: [...state.tabOrder, tab.id],
          activeTabId: state.activeTabId ?? tab.id,
        })),

      removeTab: (tabId) =>
        set((state) => {
          const { [tabId]: _, ...remainingTabs } = state.tabs
          const newOrder = state.tabOrder.filter((id) => id !== tabId)
          let newActiveId = state.activeTabId
          if (state.activeTabId === tabId) {
            const oldIndex = state.tabOrder.indexOf(tabId)
            newActiveId = newOrder[Math.min(oldIndex, newOrder.length - 1)] ?? null
          }
          return { tabs: remainingTabs, tabOrder: newOrder, activeTabId: newActiveId }
        }),

      setActiveTab: (tabId) =>
        set({ activeTabId: tabId }),

      reorderTabs: (order) =>
        set({ tabOrder: order }),

      updateTab: (tabId, updates) =>
        set((state) => ({
          tabs: { ...state.tabs, [tabId]: { ...state.tabs[tabId], ...updates } },
        })),

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
      }),
    },
  ),
)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/stores/useTabStore.test.ts`
Expected: PASS (9 tests)

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/useTabStore.ts spa/src/stores/useTabStore.test.ts
git commit -m "feat: add useTabStore for multi-tab state management"
```

---

## Task 3: useWorkspaceStore

**Files:**
- Create: `spa/src/stores/useWorkspaceStore.ts`
- Test: `spa/src/stores/useWorkspaceStore.test.ts`

- [ ] **Step 1: Write failing tests**

```typescript
// spa/src/stores/useWorkspaceStore.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { useWorkspaceStore } from './useWorkspaceStore'

describe('useWorkspaceStore', () => {
  beforeEach(() => {
    useWorkspaceStore.getState().reset()
  })

  it('initializes with a default workspace', () => {
    const state = useWorkspaceStore.getState()
    expect(state.workspaces.length).toBe(1)
    expect(state.workspaces[0].name).toBe('Default')
    expect(state.activeWorkspaceId).toBe(state.workspaces[0].id)
  })

  it('adds a tab to workspace', () => {
    const wsId = useWorkspaceStore.getState().workspaces[0].id
    useWorkspaceStore.getState().addTabToWorkspace(wsId, 'tab-1')

    const ws = useWorkspaceStore.getState().workspaces[0]
    expect(ws.tabs).toContain('tab-1')
  })

  it('removes a tab from workspace', () => {
    const wsId = useWorkspaceStore.getState().workspaces[0].id
    useWorkspaceStore.getState().addTabToWorkspace(wsId, 'tab-1')
    useWorkspaceStore.getState().removeTabFromWorkspace(wsId, 'tab-1')

    const ws = useWorkspaceStore.getState().workspaces[0]
    expect(ws.tabs).not.toContain('tab-1')
  })

  it('switches active workspace', () => {
    const ws2 = useWorkspaceStore.getState().addWorkspace('Project B')
    useWorkspaceStore.getState().setActiveWorkspace(ws2.id)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws2.id)
  })

  it('adds a workspace', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('New WS')
    expect(ws.name).toBe('New WS')
    expect(useWorkspaceStore.getState().workspaces).toHaveLength(2)
  })

  it('removes a workspace', () => {
    const ws2 = useWorkspaceStore.getState().addWorkspace('To Remove')
    useWorkspaceStore.getState().removeWorkspace(ws2.id)
    expect(useWorkspaceStore.getState().workspaces).toHaveLength(1)
  })

  it('cannot remove the last workspace', () => {
    const wsId = useWorkspaceStore.getState().workspaces[0].id
    useWorkspaceStore.getState().removeWorkspace(wsId)
    expect(useWorkspaceStore.getState().workspaces).toHaveLength(1)
  })

  it('finds workspace containing a tab', () => {
    const wsId = useWorkspaceStore.getState().workspaces[0].id
    useWorkspaceStore.getState().addTabToWorkspace(wsId, 'tab-1')
    expect(useWorkspaceStore.getState().findWorkspaceByTab('tab-1')?.id).toBe(wsId)
    expect(useWorkspaceStore.getState().findWorkspaceByTab('tab-unknown')).toBeNull()
  })

  it('sets workspace active tab', () => {
    const wsId = useWorkspaceStore.getState().workspaces[0].id
    useWorkspaceStore.getState().addTabToWorkspace(wsId, 'tab-1')
    useWorkspaceStore.getState().setWorkspaceActiveTab(wsId, 'tab-1')
    const ws = useWorkspaceStore.getState().workspaces.find(w => w.id === wsId)!
    expect(ws.activeTabId).toBe('tab-1')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/stores/useWorkspaceStore.test.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Implement useWorkspaceStore**

```typescript
// spa/src/stores/useWorkspaceStore.ts
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
          workspaces: state.workspaces.map((ws) =>
            ws.id === wsId ? { ...ws, tabs: [...ws.tabs, tabId] } : ws,
          ),
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/stores/useWorkspaceStore.test.ts`
Expected: PASS (8 tests)

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/useWorkspaceStore.ts spa/src/stores/useWorkspaceStore.test.ts
git commit -m "feat: add useWorkspaceStore for workspace + tab grouping"
```

---

## Task 4: useIsMobile hook

**Files:**
- Create: `spa/src/hooks/useIsMobile.ts`
- Test: `spa/src/hooks/useIsMobile.test.ts`

- [ ] **Step 1: Write failing test**

```typescript
// spa/src/hooks/useIsMobile.test.ts
import { describe, it, expect, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useIsMobile } from './useIsMobile'

describe('useIsMobile', () => {
  it('returns false for wide viewport', () => {
    vi.stubGlobal('matchMedia', vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    })))

    const { result } = renderHook(() => useIsMobile())
    expect(result.current).toBe(false)

    vi.unstubAllGlobals()
  })

  it('returns true for narrow viewport', () => {
    vi.stubGlobal('matchMedia', vi.fn().mockImplementation((query: string) => ({
      matches: true,
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    })))

    const { result } = renderHook(() => useIsMobile())
    expect(result.current).toBe(true)

    vi.unstubAllGlobals()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/hooks/useIsMobile.test.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Implement useIsMobile**

```typescript
// spa/src/hooks/useIsMobile.ts
import { useState, useEffect } from 'react'

const MOBILE_BREAKPOINT = 768

export function useIsMobile(): boolean {
  const [isMobile, setIsMobile] = useState(() => {
    if (typeof window === 'undefined') return false
    return window.matchMedia(`(max-width: ${MOBILE_BREAKPOINT - 1}px)`).matches
  })

  useEffect(() => {
    const mql = window.matchMedia(`(max-width: ${MOBILE_BREAKPOINT - 1}px)`)
    const handler = (e: MediaQueryListEvent) => setIsMobile(e.matches)
    mql.addEventListener('change', handler)
    return () => mql.removeEventListener('change', handler)
  }, [])

  return isMobile
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/hooks/useIsMobile.test.ts`
Expected: PASS (2 tests)

- [ ] **Step 5: Commit**

```bash
git add spa/src/hooks/useIsMobile.ts spa/src/hooks/useIsMobile.test.ts
git commit -m "feat: add useIsMobile hook for responsive breakpoints"
```

---

## Task 5: ActivityBar component

**Files:**
- Create: `spa/src/components/ActivityBar.tsx`
- Test: `spa/src/components/ActivityBar.test.tsx`

- [ ] **Step 1: Write failing tests**

```typescript
// spa/src/components/ActivityBar.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { ActivityBar } from './ActivityBar'

const mockWorkspaces = [
  { id: 'ws-1', name: 'Project A', color: '#7a6aaa', icon: '🔧', tabs: ['t1', 't2'], activeTabId: 't1', directories: [], sidebarState: {} },
  { id: 'ws-2', name: 'Server', color: '#6aaa7a', icon: '🖥', tabs: ['t3'], activeTabId: 't3', directories: [], sidebarState: {} },
]

const mockStandaloneTabs = [
  { id: 'st-1', type: 'terminal' as const, label: 'misc', icon: 'Terminal', hostId: 'mlab' },
]

describe('ActivityBar', () => {
  it('renders workspace icons', () => {
    render(
      <ActivityBar
        workspaces={mockWorkspaces as any}
        standaloneTabs={mockStandaloneTabs}
        activeWorkspaceId="ws-1"
        activeStandaloneTabId={null}
        onSelectWorkspace={vi.fn()}
        onSelectStandaloneTab={vi.fn()}
        onAddWorkspace={vi.fn()}
        onOpenSettings={vi.fn()}
      />,
    )
    expect(screen.getByTitle('Project A')).toBeTruthy()
    expect(screen.getByTitle('Server')).toBeTruthy()
  })

  it('highlights active workspace', () => {
    render(
      <ActivityBar
        workspaces={mockWorkspaces as any}
        standaloneTabs={mockStandaloneTabs}
        activeWorkspaceId="ws-1"
        activeStandaloneTabId={null}
        onSelectWorkspace={vi.fn()}
        onSelectStandaloneTab={vi.fn()}
        onAddWorkspace={vi.fn()}
        onOpenSettings={vi.fn()}
      />,
    )
    const activeBtn = screen.getByTitle('Project A')
    expect(activeBtn.className).toContain('ring')
  })

  it('calls onSelectWorkspace on click', () => {
    const onSelect = vi.fn()
    render(
      <ActivityBar
        workspaces={mockWorkspaces as any}
        standaloneTabs={mockStandaloneTabs}
        activeWorkspaceId="ws-1"
        activeStandaloneTabId={null}
        onSelectWorkspace={onSelect}
        onSelectStandaloneTab={vi.fn()}
        onAddWorkspace={vi.fn()}
        onOpenSettings={vi.fn()}
      />,
    )
    fireEvent.click(screen.getByTitle('Server'))
    expect(onSelect).toHaveBeenCalledWith('ws-2')
  })

  it('renders standalone tabs below separator', () => {
    render(
      <ActivityBar
        workspaces={mockWorkspaces as any}
        standaloneTabs={mockStandaloneTabs}
        activeWorkspaceId="ws-1"
        activeStandaloneTabId={null}
        onSelectWorkspace={vi.fn()}
        onSelectStandaloneTab={vi.fn()}
        onAddWorkspace={vi.fn()}
        onOpenSettings={vi.fn()}
      />,
    )
    expect(screen.getByTitle('misc')).toBeTruthy()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/components/ActivityBar.test.tsx`
Expected: FAIL — module not found

- [ ] **Step 3: Implement ActivityBar**

```typescript
// spa/src/components/ActivityBar.tsx
import { Plus, GearSix } from '@phosphor-icons/react'
import type { Tab, Workspace } from '../types/tab'

interface Props {
  workspaces: Workspace[]
  standaloneTabs: Tab[]
  activeWorkspaceId: string | null
  activeStandaloneTabId: string | null
  onSelectWorkspace: (wsId: string) => void
  onSelectStandaloneTab: (tabId: string) => void
  onAddWorkspace: () => void
  onOpenSettings: () => void
}

export function ActivityBar({
  workspaces,
  standaloneTabs,
  activeWorkspaceId,
  activeStandaloneTabId,
  onSelectWorkspace,
  onSelectStandaloneTab,
  onAddWorkspace,
  onOpenSettings,
}: Props) {
  return (
    <div className="hidden lg:flex w-11 flex-col items-center bg-[#08081a] border-r border-gray-800 py-2 gap-2 flex-shrink-0">
      {/* Workspaces */}
      {workspaces.map((ws) => (
        <button
          key={ws.id}
          title={ws.name}
          onClick={() => onSelectWorkspace(ws.id)}
          className={`w-8 h-8 rounded-md flex items-center justify-center text-xs cursor-pointer transition-all ${
            activeWorkspaceId === ws.id && !activeStandaloneTabId
              ? 'ring-2 ring-purple-400'
              : 'opacity-70 hover:opacity-100'
          }`}
          style={{ backgroundColor: ws.color + '33', color: ws.color }}
        >
          {ws.icon ?? ws.name.charAt(0)}
        </button>
      ))}

      {/* Separator */}
      {standaloneTabs.length > 0 && (
        <div className="w-5 h-px bg-gray-700 my-1" />
      )}

      {/* Standalone tabs */}
      {standaloneTabs.map((tab) => (
        <button
          key={tab.id}
          title={tab.label}
          onClick={() => onSelectStandaloneTab(tab.id)}
          className={`w-8 h-8 rounded-md flex items-center justify-center text-xs cursor-pointer transition-all ${
            activeStandaloneTabId === tab.id
              ? 'ring-2 ring-purple-400 bg-gray-800'
              : 'bg-gray-900 opacity-70 hover:opacity-100'
          }`}
        >
          {tab.label.charAt(0).toUpperCase()}
        </button>
      ))}

      {/* Add + Settings */}
      <div className="mt-auto flex flex-col items-center gap-2 pb-1">
        <button
          title="新增工作區"
          onClick={onAddWorkspace}
          className="w-8 h-8 rounded-md flex items-center justify-center text-gray-600 hover:text-gray-400 hover:bg-gray-800 cursor-pointer"
        >
          <Plus size={16} />
        </button>
        <button
          title="設定"
          onClick={onOpenSettings}
          className="w-8 h-8 rounded-md flex items-center justify-center text-gray-600 hover:text-gray-400 hover:bg-gray-800 cursor-pointer"
        >
          <GearSix size={16} />
        </button>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/components/ActivityBar.test.tsx`
Expected: PASS (4 tests)

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/ActivityBar.tsx spa/src/components/ActivityBar.test.tsx
git commit -m "feat: add ActivityBar component for workspace switching"
```

---

## Task 6: TabBar component

**Files:**
- Create: `spa/src/components/TabBar.tsx`
- Test: `spa/src/components/TabBar.test.tsx`

- [ ] **Step 1: Write failing tests**

```typescript
// spa/src/components/TabBar.test.tsx
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { TabBar } from './TabBar'
import type { Tab } from '../types/tab'

const mockTabs: Tab[] = [
  { id: 't1', type: 'terminal', label: 'dev-server', icon: 'Terminal', hostId: 'mlab', sessionName: 'dev' },
  { id: 't2', type: 'stream', label: 'claude', icon: 'ChatCircleDots', hostId: 'mlab', sessionName: 'claude' },
  { id: 't3', type: 'editor', label: 'App.tsx', icon: 'File', hostId: 'mlab', filePath: '/App.tsx', isDirty: true },
]

describe('TabBar', () => {
  it('renders all tabs', () => {
    render(
      <TabBar tabs={mockTabs} activeTabId="t1" onSelectTab={vi.fn()} onCloseTab={vi.fn()} onAddTab={vi.fn()} />,
    )
    expect(screen.getByText('dev-server')).toBeTruthy()
    expect(screen.getByText('claude')).toBeTruthy()
    expect(screen.getByText('App.tsx')).toBeTruthy()
  })

  it('highlights active tab', () => {
    render(
      <TabBar tabs={mockTabs} activeTabId="t1" onSelectTab={vi.fn()} onCloseTab={vi.fn()} onAddTab={vi.fn()} />,
    )
    const activeTab = screen.getByText('dev-server').closest('button')!
    expect(activeTab.className).toContain('border-b')
  })

  it('calls onSelectTab on click', () => {
    const onSelect = vi.fn()
    render(
      <TabBar tabs={mockTabs} activeTabId="t1" onSelectTab={onSelect} onCloseTab={vi.fn()} onAddTab={vi.fn()} />,
    )
    fireEvent.click(screen.getByText('claude'))
    expect(onSelect).toHaveBeenCalledWith('t2')
  })

  it('calls onCloseTab on close button click', () => {
    const onClose = vi.fn()
    render(
      <TabBar tabs={mockTabs} activeTabId="t1" onSelectTab={vi.fn()} onCloseTab={onClose} onAddTab={vi.fn()} />,
    )
    const closeButtons = screen.getAllByTitle('關閉分頁')
    fireEvent.click(closeButtons[0])
    expect(onClose).toHaveBeenCalledWith('t1')
  })

  it('shows dirty indicator for modified editor tabs', () => {
    render(
      <TabBar tabs={mockTabs} activeTabId="t1" onSelectTab={vi.fn()} onCloseTab={vi.fn()} onAddTab={vi.fn()} />,
    )
    const dirtyTab = screen.getByText('App.tsx').closest('button')!
    expect(dirtyTab.textContent).toContain('●')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/components/TabBar.test.tsx`
Expected: FAIL — module not found

- [ ] **Step 3: Implement TabBar**

```typescript
// spa/src/components/TabBar.tsx
import { X, Plus, Terminal, ChatCircleDots, File as FileIcon } from '@phosphor-icons/react'
import type { Tab } from '../types/tab'

interface Props {
  tabs: Tab[]
  activeTabId: string | null
  onSelectTab: (tabId: string) => void
  onCloseTab: (tabId: string) => void
  onAddTab: () => void
}

const ICON_MAP: Record<string, React.ComponentType<{ size: number; className?: string }>> = {
  Terminal,
  ChatCircleDots,
  File: FileIcon,
  FileCode: FileIcon,
}

function TabIcon({ icon, size = 14 }: { icon: string; size?: number }) {
  const Component = ICON_MAP[icon]
  if (!Component) return null
  return <Component size={size} className="flex-shrink-0" />
}

export function TabBar({ tabs, activeTabId, onSelectTab, onCloseTab, onAddTab }: Props) {
  return (
    <div className="flex bg-[#12122a] border-b border-gray-800 h-9 items-center px-1 gap-0.5 overflow-x-auto flex-shrink-0">
      {tabs.map((tab) => {
        const isActive = tab.id === activeTabId
        return (
          <button
            key={tab.id}
            onClick={() => onSelectTab(tab.id)}
            className={`group flex items-center gap-1.5 px-3 h-full text-xs whitespace-nowrap cursor-pointer transition-colors ${
              isActive
                ? 'text-white border-b-2 border-purple-400'
                : 'text-gray-500 hover:text-gray-300'
            }`}
          >
            <TabIcon icon={tab.icon} />
            <span>{tab.label}</span>
            {tab.isDirty && <span className="text-amber-400 text-[10px]">●</span>}
            <span
              title="關閉分頁"
              onClick={(e) => { e.stopPropagation(); onCloseTab(tab.id) }}
              className="ml-1 opacity-0 group-hover:opacity-100 hover:text-red-400 transition-opacity"
            >
              <X size={12} />
            </span>
          </button>
        )
      })}
      <button
        onClick={onAddTab}
        className="flex items-center justify-center w-7 h-7 text-gray-600 hover:text-gray-400 cursor-pointer flex-shrink-0"
        title="新增分頁"
      >
        <Plus size={14} />
      </button>
    </div>
  )
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/components/TabBar.test.tsx`
Expected: PASS (5 tests)

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/TabBar.tsx spa/src/components/TabBar.test.tsx
git commit -m "feat: add TabBar component for horizontal tab switching"
```

---

## Task 7: TabContent component

**Files:**
- Create: `spa/src/components/TabContent.tsx`
- Test: `spa/src/components/TabContent.test.tsx`

- [ ] **Step 1: Write failing tests**

```typescript
// spa/src/components/TabContent.test.tsx
import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { TabContent } from './TabContent'
import type { Tab } from '../types/tab'

// Mock heavy components
vi.mock('./TerminalView', () => ({
  default: ({ wsUrl }: { wsUrl: string }) => <div data-testid="terminal-view">Terminal: {wsUrl}</div>,
}))
vi.mock('./ConversationView', () => ({
  default: ({ sessionName }: { sessionName: string }) => <div data-testid="conversation-view">Stream: {sessionName}</div>,
}))

describe('TabContent', () => {
  it('renders TerminalView for terminal tab', () => {
    const tab: Tab = { id: 't1', type: 'terminal', label: 'dev', icon: 'Terminal', hostId: 'mlab', sessionName: 'dev' }
    render(<TabContent tab={tab} wsBase="ws://test" daemonBase="http://test" />)
    expect(screen.getByTestId('terminal-view')).toBeTruthy()
  })

  it('renders ConversationView for stream tab', () => {
    const tab: Tab = { id: 't2', type: 'stream', label: 'claude', icon: 'ChatCircleDots', hostId: 'mlab', sessionName: 'claude' }
    render(<TabContent tab={tab} wsBase="ws://test" daemonBase="http://test" />)
    expect(screen.getByTestId('conversation-view')).toBeTruthy()
  })

  it('renders placeholder for editor tab', () => {
    const tab: Tab = { id: 't3', type: 'editor', label: 'file.ts', icon: 'File', hostId: 'mlab', filePath: '/file.ts' }
    render(<TabContent tab={tab} wsBase="ws://test" daemonBase="http://test" />)
    expect(screen.getByText(/file\.ts/)).toBeTruthy()
  })

  it('renders empty state when no tab', () => {
    render(<TabContent tab={null} wsBase="ws://test" daemonBase="http://test" />)
    expect(screen.getByText(/選擇或建立/)).toBeTruthy()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/components/TabContent.test.tsx`
Expected: FAIL — module not found

- [ ] **Step 3: Implement TabContent**

```typescript
// spa/src/components/TabContent.tsx
import { lazy, Suspense } from 'react'
import type { Tab } from '../types/tab'

const TerminalView = lazy(() => import('./TerminalView'))
const ConversationView = lazy(() => import('./ConversationView'))

interface Props {
  tab: Tab | null
  wsBase: string
  daemonBase: string
  onHandoff?: () => void
  onHandoffToTerm?: () => void
}

export function TabContent({ tab, wsBase, daemonBase, onHandoff, onHandoffToTerm }: Props) {
  if (!tab) {
    return (
      <div className="flex-1 flex items-center justify-center text-gray-600 text-sm">
        選擇或建立一個分頁開始使用
      </div>
    )
  }

  return (
    <div className="flex-1 overflow-hidden relative">
      <Suspense fallback={<div className="flex-1 flex items-center justify-center text-gray-600">Loading...</div>}>
        {tab.type === 'terminal' && tab.sessionName && (
          <TerminalView
            wsUrl={`${wsBase}/ws/terminal/${tab.sessionName}`}
            visible={true}
          />
        )}
        {tab.type === 'stream' && tab.sessionName && (
          <ConversationView
            sessionName={tab.sessionName}
            onHandoff={onHandoff}
            onHandoffToTerm={onHandoffToTerm}
          />
        )}
        {tab.type === 'editor' && (
          <div className="flex-1 flex items-center justify-center text-gray-500 text-sm">
            Editor: {tab.filePath ?? tab.label}（Phase 5 實作）
          </div>
        )}
      </Suspense>
    </div>
  )
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/components/TabContent.test.tsx`
Expected: PASS (4 tests)

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/TabContent.tsx spa/src/components/TabContent.test.tsx
git commit -m "feat: add TabContent component for dynamic tab rendering"
```

---

## Task 8: StatusBar component

**Files:**
- Create: `spa/src/components/StatusBar.tsx`
- Test: `spa/src/components/StatusBar.test.tsx`

- [ ] **Step 1: Write failing test**

```typescript
// spa/src/components/StatusBar.test.tsx
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { StatusBar } from './StatusBar'

describe('StatusBar', () => {
  it('renders host and session info', () => {
    render(<StatusBar hostName="mlab" sessionName="dev-server" status="connected" mode="term" />)
    expect(screen.getByText('mlab')).toBeTruthy()
    expect(screen.getByText('dev-server')).toBeTruthy()
    expect(screen.getByText('connected')).toBeTruthy()
    expect(screen.getByText('term')).toBeTruthy()
  })

  it('renders empty state when no session', () => {
    render(<StatusBar hostName={null} sessionName={null} status={null} mode={null} />)
    expect(screen.getByText('No active session')).toBeTruthy()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/components/StatusBar.test.tsx`
Expected: FAIL — module not found

- [ ] **Step 3: Implement StatusBar**

```typescript
// spa/src/components/StatusBar.tsx
interface Props {
  hostName: string | null
  sessionName: string | null
  status: string | null
  mode: string | null
}

export function StatusBar({ hostName, sessionName, status, mode }: Props) {
  if (!sessionName) {
    return (
      <div className="h-6 bg-[#12122a] border-t border-gray-800 flex items-center px-3 text-[10px] text-gray-600 flex-shrink-0">
        No active session
      </div>
    )
  }

  return (
    <div className="h-6 bg-[#12122a] border-t border-gray-800 flex items-center px-3 text-[10px] text-gray-600 gap-3 flex-shrink-0">
      <span>{hostName}</span>
      <span>{sessionName}</span>
      <span className={status === 'connected' ? 'text-green-500' : 'text-gray-600'}>
        {status}
      </span>
      <span className="ml-auto">{mode}</span>
    </div>
  )
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/components/StatusBar.test.tsx`
Expected: PASS (2 tests)

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/StatusBar.tsx spa/src/components/StatusBar.test.tsx
git commit -m "feat: add StatusBar component for connection info display"
```

---

## Task 9: App.tsx 重構

這是最關鍵的 task——重構 App.tsx 以整合所有新元件。

**Files:**
- Modify: `spa/src/App.tsx` (full rewrite)
- Modify: `spa/src/components/TerminalView.tsx` (add default export)
- Modify: `spa/src/components/ConversationView.tsx` (add default export)

- [ ] **Step 1: 確認 TerminalView 和 ConversationView 有 default export**

檢查 `spa/src/components/TerminalView.tsx` 和 `spa/src/components/ConversationView.tsx` 是否有 default export。如果是 named export，需要加上 `export default`。TabContent 使用 `lazy(() => import('./TerminalView'))` 需要 default export。

若沒有 default export，在檔案最後加：
```typescript
export default TerminalView  // 或 ConversationView
```

- [ ] **Step 2: 重構 App.tsx**

將 App.tsx 完全改寫為新的佈局結構。保留所有現有功能（session fetch、session-events WS、handoff 邏輯），但將 UI 從 SessionPanel+TopBar+Content 改為 ActivityBar+TabBar+TabContent+StatusBar。

核心改動：
- 移除 TopBar 引用，功能由 TabBar 取代
- 移除 SessionPanel 引用（Phase 3 重新引入為側欄面板）
- 新增 migration 邏輯：首次載入時，將現有 sessions 轉為 tabs
- Hash routing 從 `#/{uid}/{mode}` 改為 `#/tab/{tabId}`

```typescript
// spa/src/App.tsx — 完整重寫
// 注意：此處只列出結構性改動，實際實作需保留所有 session-events、handoff、stream 邏輯

import { useEffect, useState, useCallback, useRef } from 'react'
import { ActivityBar } from './components/ActivityBar'
import { TabBar } from './components/TabBar'
import { TabContent } from './components/TabContent'
import { StatusBar } from './components/StatusBar'
import { SettingsPanel } from './components/SettingsPanel'
import { useSessionStore } from './stores/useSessionStore'
import { useStreamStore } from './stores/useStreamStore'
import { useConfigStore } from './stores/useConfigStore'
import { useTabStore } from './stores/useTabStore'
import { useWorkspaceStore } from './stores/useWorkspaceStore'
import { useRelayWsManager } from './hooks/useRelayWsManager'
import { useIsMobile } from './hooks/useIsMobile'
import { connectSessionEvents } from './lib/session-events'
import { createTab, isStandaloneTab } from './types/tab'
import type { Tab } from './types/tab'

const daemonBase = 'http://100.64.0.2:7860'  // TODO: from config/localStorage
const wsBase = daemonBase.replace(/^http/, 'ws')

function parseHash(): { tabId: string | null } {
  const hash = window.location.hash.replace(/^#\/?/, '')
  if (!hash) return { tabId: null }
  const parts = hash.split('/')
  if (parts[0] === 'tab' && parts[1]) return { tabId: parts[1] }
  return { tabId: null }
}

function setHash(tabId: string) {
  window.location.hash = `#/tab/${tabId}`
}

export default function App() {
  const isMobile = useIsMobile()
  const [settingsOpen, setSettingsOpen] = useState(false)

  // Existing stores
  const { sessions, fetch: fetchSessions } = useSessionStore()
  const config = useConfigStore((s) => s.config)
  const fetchConfig = useConfigStore((s) => s.fetch)

  // New stores
  const { tabs, tabOrder, activeTabId, addTab, removeTab, setActiveTab, getActiveTab } = useTabStore()
  const {
    workspaces, activeWorkspaceId,
    setActiveWorkspace, addTabToWorkspace,
    removeTabFromWorkspace, findWorkspaceByTab, addWorkspace,
  } = useWorkspaceStore()

  // Stream store (kept for session-events compatibility)
  const setRelayStatus = useStreamStore((s) => s.setRelayStatus)
  const setSessionStatus = useStreamStore((s) => s.setSessionStatus)
  const setHandoffProgress = useStreamStore((s) => s.setHandoffProgress)

  // Relay WS manager (unchanged — accepts only wsBase)
  useRelayWsManager(wsBase)

  // Active tab derived state
  const activeTab = getActiveTab()

  // --- Bootstrap: fetch sessions, config, connect events ---
  useEffect(() => {
    fetchSessions(daemonBase)
    fetchConfig(daemonBase)
  }, [fetchSessions, fetchConfig])

  // Session events WS (unchanged logic from original App.tsx)
  useEffect(() => {
    const conn = connectSessionEvents(
      `${wsBase}/ws/session-events`,
      (event) => {
        if (event.type === 'status') {
          setSessionStatus(event.session, event.value as any)
          fetchSessions(daemonBase)
        } else if (event.type === 'relay') {
          setRelayStatus(event.session, event.value === 'connected')
        } else if (event.type === 'handoff') {
          setHandoffProgress(event.session, event.value)
        }
      },
    )
    return () => conn.close()
  }, [setRelayStatus, setSessionStatus, setHandoffProgress, fetchSessions])

  // --- Migration: on first load, if no tabs exist, create tabs from sessions ---
  const migrated = useRef(false)
  useEffect(() => {
    if (migrated.current || sessions.length === 0 || tabOrder.length > 0) return
    migrated.current = true

    const defaultWsId = workspaces[0]?.id
    sessions.forEach((s) => {
      const tab = createTab({
        type: s.mode === 'stream' ? 'stream' : 'terminal',
        label: s.name,
        hostId: 'local',
        sessionName: s.name,
      })
      addTab(tab)
      if (defaultWsId) addTabToWorkspace(defaultWsId, tab.id)
    })
  }, [sessions, tabOrder.length, workspaces, addTab, addTabToWorkspace])

  // --- Hash routing ---
  useEffect(() => {
    const { tabId } = parseHash()
    if (tabId && tabs[tabId]) {
      setActiveTab(tabId)
    }
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (activeTabId) setHash(activeTabId)
  }, [activeTabId])

  useEffect(() => {
    const handler = () => {
      const { tabId } = parseHash()
      if (tabId && tabs[tabId]) setActiveTab(tabId)
    }
    window.addEventListener('hashchange', handler)
    return () => window.removeEventListener('hashchange', handler)
  }, [tabs, setActiveTab])

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
    if (ws) setActiveWorkspace(ws.id)
  }, [setActiveTab, findWorkspaceByTab, setActiveWorkspace])

  const handleCloseTab = useCallback((tabId: string) => {
    const ws = findWorkspaceByTab(tabId)
    if (ws) removeTabFromWorkspace(ws.id, tabId)
    removeTab(tabId)
  }, [findWorkspaceByTab, removeTabFromWorkspace, removeTab])

  const handleAddTab = useCallback(() => {
    // Phase 1: opens Quick Switcher placeholder or creates a blank tab
    // For now, just open settings where sessions can be managed
    setSettingsOpen(true)
  }, [])

  const handleAddWorkspace = useCallback(() => {
    const ws = addWorkspace('New Workspace')
    setActiveWorkspace(ws.id)
  }, [addWorkspace, setActiveWorkspace])

  // --- Derive visible tabs for current context ---
  const activeWs = workspaces.find((w) => w.id === activeWorkspaceId)
  const visibleTabs: Tab[] = activeWs
    ? activeWs.tabs.map((id) => tabs[id]).filter(Boolean)
    : []

  // Standalone tabs
  const standaloneTabs = tabOrder
    .filter((id) => isStandaloneTab(id, workspaces))
    .map((id) => tabs[id])
    .filter(Boolean)

  // Active standalone tab
  const activeStandaloneTabId = activeTabId && isStandaloneTab(activeTabId, workspaces) ? activeTabId : null

  // If viewing standalone tab, show it in TabBar too
  const displayTabs = activeStandaloneTabId
    ? [tabs[activeStandaloneTabId]].filter(Boolean)
    : visibleTabs

  // StatusBar info
  const statusHost = activeTab?.hostId === 'local' ? 'mlab' : activeTab?.hostId ?? null
  const statusSession = activeTab?.sessionName ?? null
  const statusMode = activeTab?.type ?? null

  return (
    <div className="h-screen flex">
      {/* Activity Bar */}
      <ActivityBar
        workspaces={workspaces}
        standaloneTabs={standaloneTabs}
        activeWorkspaceId={activeStandaloneTabId ? null : activeWorkspaceId}
        activeStandaloneTabId={activeStandaloneTabId}
        onSelectWorkspace={handleSelectWorkspace}
        onSelectStandaloneTab={handleSelectTab}
        onAddWorkspace={handleAddWorkspace}
        onOpenSettings={() => setSettingsOpen(true)}
      />

      {/* Main area */}
      <div className="flex-1 flex flex-col min-w-0">
        {/* Tab bar */}
        <TabBar
          tabs={displayTabs}
          activeTabId={activeTabId}
          onSelectTab={handleSelectTab}
          onCloseTab={handleCloseTab}
          onAddTab={handleAddTab}
        />

        {/* Content area (sidebar zones as empty shells for Phase 3) */}
        <div className="flex-1 flex overflow-hidden">
          {/* Left sidebar zones placeholder */}
          {/* <div className="hidden" id="sidebar-left-outer" /> */}
          {/* <div className="hidden" id="sidebar-left-inner" /> */}

          {/* Content */}
          <TabContent
            tab={activeTab}
            wsBase={wsBase}
            daemonBase={daemonBase}
          />

          {/* Right sidebar zones placeholder */}
          {/* <div className="hidden" id="sidebar-right-inner" /> */}
          {/* <div className="hidden" id="sidebar-right-outer" /> */}
        </div>

        {/* Status bar */}
        <StatusBar
          hostName={statusHost}
          sessionName={statusSession}
          status={activeTab ? 'connected' : null}
          mode={statusMode}
        />
      </div>

      {/* Settings Panel — props 必須匹配實際 interface: { daemonBase, onClose, onTerminalReconnect? } */}
      {settingsOpen && (
        <SettingsPanel
          daemonBase={daemonBase}
          onClose={() => setSettingsOpen(false)}
          onTerminalReconnect={handleTerminalReconnect}
        />
      )}
    </div>
  )
}
```

> **重要實作注意事項：**
>
> 1. **SettingsPanel** 不接受 `config` prop（它內部透過 `useConfigStore` 讀取），只需 `daemonBase`、`onClose`、`onTerminalReconnect`。
>
> 2. **Handoff 邏輯必須完整保留。** 上方程式碼是佈局骨架，實際實作時必須從現有 `App.tsx` 搬移以下邏輯：
>    - `handleHandoff(sessionId, preset?)` — 呼叫 `handoff()` API、管理 presets、設定 progress
>    - `handleHandoffToTerm(sessionId)` — 切換 mode 為 term、呼叫 `handoff()` API
>    - `handleTerminalReconnect()` — 重置 terminalKey 觸發 TerminalView 重新掛載
>    - `activePreset` state 和 `streamPresets` 從 config 推導
>    - Session-events handler 的完整版本（包含 `fetchHistory`、`clearSession`、relay status 後的 history 載入）
>    - `terminalKey` / `connectingMessage` 機制（terminal 重連後顯示自訂訊息）
>
>    **做法：** 逐段從現有 `App.tsx` 搬移，而非從頭寫。只改佈局結構，業務邏輯原封搬移。
>
> 3. **TabContent 必須接收 handoff callbacks。** App.tsx 需要傳遞 `onHandoff` 和 `onHandoffToTerm` 給 TabContent：
>    ```tsx
>    <TabContent
>      tab={activeTab}
>      wsBase={wsBase}
>      daemonBase={daemonBase}
>      onHandoff={() => handleHandoff(activeSession?.id)}
>      onHandoffToTerm={() => handleHandoffToTerm(activeSession?.id)}
>    />
>    ```

- [ ] **Step 3: 確認 TerminalView/ConversationView 的 props 相容性**

讀取 `TerminalView.tsx` 和 `ConversationView.tsx` 確認 TabContent 傳入的 props 完全相容：
- TerminalView: 需要 `wsUrl: string`, `visible?: boolean`
- ConversationView: 需要 `sessionName: string`, `onHandoff?`, `onHandoffToTerm?`

- [ ] **Step 4: Run all tests**

Run: `cd spa && npx vitest run`
Expected: 所有測試通過（既有測試 + 新測試）

如果有測試失敗，逐一修復。常見問題：
- 舊的 App 相關測試可能引用 TopBar、SessionPanel
- SettingsPanel props 可能不匹配

- [ ] **Step 5: 手動驗證**

Run: `cd spa && npm run dev`
在瀏覽器中確認：
1. Activity Bar 顯示在最左側
2. TabBar 顯示 sessions 對應的分頁
3. 點擊分頁可切換 Terminal/Stream 內容
4. 關閉分頁功能正常
5. StatusBar 顯示當前連線資訊
6. 設定面板仍可開啟

- [ ] **Step 6: Commit**

```bash
git add spa/src/App.tsx spa/src/components/TerminalView.tsx spa/src/components/ConversationView.tsx
git commit -m "feat: restructure App layout with ActivityBar + TabBar + TabContent + StatusBar"
```

---

## Task 10: 移除舊的 TopBar，清理引用

**Files:**
- Modify: `spa/src/components/TopBar.tsx` — 保留檔案但標記 deprecated（Phase 完成確認後可刪除）
- Delete or update: 相關測試

- [ ] **Step 1: 確認 TopBar 不再被引用**

搜尋 `spa/src/` 中所有 `TopBar` 引用，確認只剩測試檔案。

Run: `cd spa && grep -r "TopBar" src/ --include="*.tsx" --include="*.ts" -l`
Expected: 只有 `TopBar.tsx` 和 `TopBar.test.tsx`

- [ ] **Step 2: 在 TopBar.tsx 加上 deprecated 註解**

```typescript
// @deprecated Phase 1 — TopBar 功能已被 TabBar 取代。確認無其他引用後可刪除。
```

- [ ] **Step 3: Run full test suite**

Run: `cd spa && npx vitest run`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add spa/src/components/TopBar.tsx
git commit -m "chore: mark TopBar as deprecated, replaced by TabBar"
```

---

## Task 11: Hash routing 向後相容

確保舊的 `#/{uid}/{mode}` hash 格式能正確 redirect 到新格式。

**Files:**
- Modify: `spa/src/App.tsx` (在 parseHash 增加 legacy 支援)

- [ ] **Step 1: 更新 parseHash 支援舊格式**

```typescript
function parseHash(): { tabId: string | null; legacyUid?: string; legacyMode?: string } {
  const hash = window.location.hash.replace(/^#\/?/, '')
  if (!hash) return { tabId: null }
  const parts = hash.split('/')

  // New format: #/tab/{tabId}
  if (parts[0] === 'tab' && parts[1]) return { tabId: parts[1] }

  // Legacy format: #/{uid}/{mode}
  if (parts[0] && parts[0] !== 'tab') {
    return { tabId: null, legacyUid: parts[0], legacyMode: parts[1] || 'term' }
  }

  return { tabId: null }
}
```

- [ ] **Step 2: 在 App.tsx 的初始化中處理 legacy hash**

在 hash routing useEffect 中，若偵測到 legacyUid，找到對應的 tab（by sessionName + matching session uid）並 redirect：

```typescript
useEffect(() => {
  const { tabId, legacyUid, legacyMode } = parseHash()
  if (tabId && tabs[tabId]) {
    setActiveTab(tabId)
  } else if (legacyUid) {
    // Find session by uid, then find tab by sessionName
    const session = sessions.find(s => s.uid === legacyUid)
    if (session) {
      const tab = Object.values(tabs).find(t => t.sessionName === session.name)
      if (tab) {
        setActiveTab(tab.id)
        setHash(tab.id) // Redirect to new format
      }
    }
  }
}, []) // eslint-disable-line
```

- [ ] **Step 3: 手動測試 legacy URL**

在瀏覽器中輸入舊格式 URL（如 `#/abc123/term`），確認正確 redirect 到對應的 tab。

- [ ] **Step 4: Commit**

```bash
git add spa/src/App.tsx
git commit -m "feat: add legacy hash routing backward compatibility"
```

---

## Task 12: 整合測試 + 最終驗證

- [ ] **Step 1: Run full test suite**

Run: `cd spa && npx vitest run`
Expected: ALL PASS

- [ ] **Step 2: Run lint**

Run: `cd spa && npm run lint`
Expected: No errors

- [ ] **Step 3: Run build**

Run: `cd spa && npm run build`
Expected: Build succeeds

- [ ] **Step 4: 手動 E2E 驗證清單**

在瀏覽器中確認以下功能：

- [ ] Activity Bar 顯示預設工作區
- [ ] TabBar 顯示從 sessions 遷移的分頁
- [ ] 點擊分頁切換 Terminal 內容（xterm.js 正常運作）
- [ ] 點擊分頁切換 Stream 內容（對話顯示正常）
- [ ] 關閉分頁後自動切換到相鄰分頁
- [ ] 關閉所有分頁顯示空狀態
- [ ] Hash URL 正確更新為 `#/tab/{id}` 格式
- [ ] 舊的 `#/{uid}/{mode}` URL 正確 redirect
- [ ] 重新整理頁面後分頁狀態保留（localStorage）
- [ ] StatusBar 顯示當前 host、session、狀態
- [ ] Settings 面板仍可開啟和操作
- [ ] Session-events WS 正常運作（狀態即時更新）
- [ ] Handoff 功能正常（stream ↔ terminal 切換）
- [ ] 手機寬度下 Activity Bar 隱藏（`hidden lg:flex`）

- [ ] **Step 5: Final commit**

```bash
git add -A
git commit -m "feat: complete Phase 1 - tab system + Activity Bar foundation"
```
