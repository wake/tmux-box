# Pin/Lock 獨立化 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 將 pin 和 lock 解耦為獨立旗標，pinned tab 可關閉，reopen 恢復 pinned 狀態，lock icon 繼承 tab 文字色。

**Architecture:** 修改 useTabStore 的 pinTab/unpinTab/unlockTab/dismissTab 行為，dismissedSessions 從 `string[]` 改為 `{ sessionName, pinned }[]`，加 persist migration v2→v3。UI 層改 TabContextMenu 條件和 SortableTab lock icon 色。

**Tech Stack:** React 19 / Zustand 5 / Vitest / Tailwind 4

**Spec:** `docs/superpowers/specs/2026-03-22-pin-lock-independence.md`

---

### Task 1: Store — pinTab 不設 locked + unlockTab 移除 pinned guard

**Files:**
- Modify: `spa/src/stores/useTabStore.ts:154-164` (pinTab)
- Modify: `spa/src/stores/useTabStore.ts:184-189` (unlockTab)
- Test: `spa/src/stores/useTabStore.pin-lock.test.ts`

- [ ] **Step 1: 更新 pinTab 測試（第 18-28 行）**

```ts
it('pinTab sets pinned=true, does not change locked', () => {
  const a = addTab('a')
  const b = addTab('b')
  const c = addTab('c')
  useTabStore.getState().pinTab(b.id)

  const state = useTabStore.getState()
  expect(state.tabs[b.id].pinned).toBe(true)
  expect(state.tabs[b.id].locked).toBe(false) // 改：不再自動 lock
  expect(state.tabOrder).toEqual([b.id, a.id, c.id])
})
```

新增測試：

```ts
it('pinTab preserves existing locked=true', () => {
  const a = addTab('a')
  useTabStore.getState().lockTab(a.id)
  useTabStore.getState().pinTab(a.id)
  expect(useTabStore.getState().tabs[a.id].pinned).toBe(true)
  expect(useTabStore.getState().tabs[a.id].locked).toBe(true)
})
```

- [ ] **Step 2: 更新 unpinTab 測試（第 30-40 行）**

```ts
it('unpinTab sets pinned=false, does not change locked', () => {
  const a = addTab('a')
  addTab('b')
  useTabStore.getState().lockTab(a.id) // 手動 lock（pinTab 不再自動 lock）
  useTabStore.getState().pinTab(a.id)
  useTabStore.getState().unpinTab(a.id)

  const state = useTabStore.getState()
  expect(state.tabs[a.id].pinned).toBe(false)
  expect(state.tabs[a.id].locked).toBe(true) // locked 不受 unpin 影響
  expect(state.tabOrder[0]).toBe(a.id)
})
```

- [ ] **Step 3: 更新 unlockTab 測試（第 81-86 行）**

```ts
it('unlockTab on pinned tab sets locked=false', () => {
  const a = addTab('a')
  useTabStore.getState().lockTab(a.id)
  useTabStore.getState().pinTab(a.id)
  useTabStore.getState().unlockTab(a.id)
  expect(useTabStore.getState().tabs[a.id].locked).toBe(false)
  expect(useTabStore.getState().tabs[a.id].pinned).toBe(true)
})
```

- [ ] **Step 4: 新增 pinned + unlocked/locked 關閉測試**

```ts
it('pinned + unlocked tab can be dismissed', () => {
  const a = addTab('a')
  useTabStore.getState().pinTab(a.id)
  useTabStore.getState().dismissTab(a.id)
  expect(useTabStore.getState().tabs[a.id]).toBeUndefined()
})

it('pinned + locked tab cannot be dismissed', () => {
  const a = addTab('a')
  useTabStore.getState().lockTab(a.id)
  useTabStore.getState().pinTab(a.id)
  useTabStore.getState().dismissTab(a.id)
  expect(useTabStore.getState().tabs[a.id]).toBeDefined()
})
```

- [ ] **Step 5: 跑測試確認 RED**

Run: `cd spa && npx vitest run src/stores/useTabStore.pin-lock.test.ts`
Expected: FAIL（pinTab locked 期望值、unlockTab pinned guard）

- [ ] **Step 6: 實作 pinTab 和 unlockTab**

`useTabStore.ts` 第 158 行：

```ts
// 舊：const updated = { ...tab, pinned: true, locked: true }
// 新：
const updated = { ...tab, pinned: true }
```

`useTabStore.ts` 第 184-189 行：

```ts
// 舊：
unlockTab: (tabId) =>
  set((state) => {
    const tab = state.tabs[tabId]
    if (!tab || tab.pinned) return state
    return { tabs: { ...state.tabs, [tabId]: { ...tab, locked: false } } }
  }),

// 新：
unlockTab: (tabId) =>
  set((state) => {
    const tab = state.tabs[tabId]
    if (!tab) return state
    return { tabs: { ...state.tabs, [tabId]: { ...tab, locked: false } } }
  }),
```

- [ ] **Step 7: 跑測試確認 GREEN**

Run: `cd spa && npx vitest run src/stores/useTabStore.pin-lock.test.ts`
Expected: ALL PASS

- [ ] **Step 8: Commit**

```
git add spa/src/stores/useTabStore.ts spa/src/stores/useTabStore.pin-lock.test.ts
git commit -m "feat(store): pinTab no longer sets locked, unlockTab works on pinned tabs"
```

---

### Task 2: Store — dismissedSessions 格式變更 + migration

**Files:**
- Modify: `spa/src/stores/useTabStore.ts:6-26` (interface + type)
- Modify: `spa/src/stores/useTabStore.ts:28-65` (migration)
- Modify: `spa/src/stores/useTabStore.ts:97-124` (dismissTab, undismissSession, isSessionDismissed)
- Modify: `spa/src/stores/useTabStore.ts:191-203` (persist version)
- Test: `spa/src/stores/useTabStore.pin-lock.test.ts`

- [ ] **Step 1: 新增測試**

```ts
describe('dismissTab stores pinned state', () => {
  beforeEach(reset)

  it('dismissTab stores pinned=false for normal tab', () => {
    const a = addTab('a')
    useTabStore.getState().dismissTab(a.id)
    const dismissed = useTabStore.getState().dismissedSessions
    expect(dismissed).toEqual([{ sessionName: 'a', pinned: false }])
  })

  it('dismissTab stores pinned=true for pinned tab', () => {
    const a = addTab('a')
    useTabStore.getState().pinTab(a.id)
    useTabStore.getState().dismissTab(a.id)
    const dismissed = useTabStore.getState().dismissedSessions
    expect(dismissed).toEqual([{ sessionName: 'a', pinned: true }])
  })

  it('undismissSession removes by sessionName', () => {
    const a = addTab('a')
    useTabStore.getState().dismissTab(a.id)
    useTabStore.getState().undismissSession('a')
    expect(useTabStore.getState().dismissedSessions).toEqual([])
  })

  it('isSessionDismissed checks by sessionName', () => {
    const a = addTab('a')
    useTabStore.getState().dismissTab(a.id)
    expect(useTabStore.getState().isSessionDismissed('a')).toBe(true)
    expect(useTabStore.getState().isSessionDismissed('nonexistent')).toBe(false)
  })
})

describe('persist migration v2→v3', () => {
  it('converts dismissedSessions string[] to object[]', () => {
    const old = {
      tabs: {},
      tabOrder: [],
      activeTabId: null,
      dismissedSessions: ['foo', 'bar'],
    }
    const result = migrateTabStore(old, 2) as any
    expect(result.dismissedSessions).toEqual([
      { sessionName: 'foo', pinned: false },
      { sessionName: 'bar', pinned: false },
    ])
  })
})
```

- [ ] **Step 2: 跑測試確認 RED**

Run: `cd spa && npx vitest run src/stores/useTabStore.pin-lock.test.ts`
Expected: new tests FAIL

- [ ] **Step 3: 實作 DismissedSession type 和 interface**

`useTabStore.ts` 頂部新增 type，修改 interface：

```ts
export interface DismissedSession {
  sessionName: string
  pinned: boolean
}

interface TabState {
  // ...
  dismissedSessions: DismissedSession[]
  // ... actions unchanged
}
```

- [ ] **Step 4: 實作 dismissTab 存 pinned**

`useTabStore.ts` `dismissTab` 第 110-113 行：

```ts
// 舊：
const dismissed = sessionName
  ? [...state.dismissedSessions, sessionName]
  : state.dismissedSessions

// 新：
const dismissed = sessionName
  ? [...state.dismissedSessions, { sessionName, pinned: tab.pinned }]
  : state.dismissedSessions
```

- [ ] **Step 5: 實作 undismissSession 和 isSessionDismissed**

```ts
undismissSession: (sessionName) =>
  set((state) => ({
    dismissedSessions: state.dismissedSessions.filter((s) => s.sessionName !== sessionName),
  })),

isSessionDismissed: (sessionName) => {
  return get().dismissedSessions.some((s) => s.sessionName === sessionName)
},
```

- [ ] **Step 6: 實作 migration v2→v3**

`migrateTabStore` 函式內新增：

```ts
if (version < 3) {
  // v2→v3: dismissedSessions string[] → { sessionName, pinned }[]
  persisted.dismissedSessions = (persisted.dismissedSessions ?? [])
    .map((s: any) => typeof s === 'string' ? { sessionName: s, pinned: false } : s)
}
```

persist config 改 `version: 3`。

- [ ] **Step 7: 跑測試確認 GREEN**

Run: `cd spa && npx vitest run src/stores/useTabStore.pin-lock.test.ts`
Expected: ALL PASS

- [ ] **Step 8: 跑全部測試**

Run: `cd spa && npx vitest run`
Expected: ALL PASS

- [ ] **Step 9: Commit**

```
git add spa/src/stores/useTabStore.ts spa/src/stores/useTabStore.pin-lock.test.ts
git commit -m "feat(store): dismissedSessions stores pinned state, migration v2→v3"
```

---

### Task 3: App.tsx — reopenClosed + handleSessionSelect 恢復 pinned

**Files:**
- Modify: `spa/src/App.tsx:185-209` (reopenClosed case)
- Modify: `spa/src/App.tsx:109-129` (handleSessionSelect)

- [ ] **Step 1: 修改 reopenClosed**

```ts
case 'reopenClosed': {
  const dismissed = store.dismissedSessions
  if (dismissed.length === 0) break
  const last = dismissed[dismissed.length - 1]
  store.undismissSession(last.sessionName)
  const existing = Object.values(store.tabs).find((t) => getSessionName(t) === last.sessionName)
  if (existing) {
    store.setActiveTab(existing.id)
  } else {
    const session = useSessionStore.getState().sessions.find((s) => s.name === last.sessionName)
    if (session) {
      const newTab = createSessionTab({
        label: session.name,
        hostId: 'local',
        sessionName: session.name,
        viewMode: session.mode === 'stream' ? 'stream' : 'terminal',
      })
      store.addTab(newTab)
      if (last.pinned) store.pinTab(newTab.id)
      store.setActiveTab(newTab.id)
      const wsId = useWorkspaceStore.getState().activeWorkspaceId
      if (wsId) {
        useWorkspaceStore.getState().addTabToWorkspace(wsId, newTab.id)
        useWorkspaceStore.getState().setWorkspaceActiveTab(wsId, newTab.id)
      }
    }
  }
  break
}
```

- [ ] **Step 2: 修改 handleSessionSelect**

`App.tsx` `handleSessionSelect`（第 109-129 行），在 `addTab(tab)` 後加 pinned 恢復邏輯：

```ts
const handleSessionSelect = useCallback((session: typeof sessions[0]) => {
  setSessionPickerOpen(false)
  const dismissedEntry = useTabStore.getState().dismissedSessions.find(
    (s) => s.sessionName === session.name
  )
  useTabStore.getState().undismissSession(session.name)
  const existing = Object.values(tabs).find((t) => getSessionName(t) === session.name)
  if (existing) {
    setActiveTab(existing.id)
    return
  }
  const tab = createSessionTab({
    label: session.name,
    hostId: 'local',
    sessionName: session.name,
    viewMode: session.mode === 'stream' ? 'stream' : 'terminal',
  })
  addTab(tab)
  if (dismissedEntry?.pinned) useTabStore.getState().pinTab(tab.id)
  setActiveTab(tab.id)
  if (activeWorkspaceId) {
    addTabToWorkspace(activeWorkspaceId, tab.id)
    setWorkspaceActiveTab(activeWorkspaceId, tab.id)
  }
}, [tabs, setActiveTab, addTab, activeWorkspaceId, addTabToWorkspace, setWorkspaceActiveTab])
```

- [ ] **Step 3: 跑全部測試確認 GREEN**

Run: `cd spa && npx vitest run`
Expected: ALL PASS

- [ ] **Step 4: Commit**

```
git add spa/src/App.tsx
git commit -m "feat(app): reopenClosed and handleSessionSelect restore pinned state"
```

---

### Task 4: TabContextMenu — 解鎖分頁條件修正

**Files:**
- Modify: `spa/src/components/TabContextMenu.tsx:65`
- Test: `spa/src/components/TabContextMenu.test.tsx`

- [ ] **Step 1: 新增測試**

```ts
it('shows "解鎖分頁" for pinned + locked tab', () => {
  renderMenu({ tab: { pinned: true, locked: true } })
  expect(screen.getByText('解鎖分頁')).toBeInTheDocument()
})
```

- [ ] **Step 2: 跑測試確認 RED**

Run: `cd spa && npx vitest run src/components/TabContextMenu.test.tsx`
Expected: new test FAIL

- [ ] **Step 3: 修改 show 條件**

`TabContextMenu.tsx` 第 65 行：

```ts
// 舊：
{ label: '解鎖分頁', action: 'unlock' as const, show: tab.locked && !tab.pinned },

// 新：
{ label: '解鎖分頁', action: 'unlock' as const, show: tab.locked },
```

- [ ] **Step 4: 跑測試確認 GREEN**

Run: `cd spa && npx vitest run src/components/TabContextMenu.test.tsx`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```
git add spa/src/components/TabContextMenu.tsx spa/src/components/TabContextMenu.test.tsx
git commit -m "feat(menu): unlock option visible for pinned tabs (pin/lock independent)"
```

---

### Task 5: SortableTab — Lock icon 繼承 tab 文字色

**Files:**
- Modify: `spa/src/components/SortableTab.tsx:101`

- [ ] **Step 1: 修改 Lock icon class**

`SortableTab.tsx` 第 101 行：

```tsx
// 舊：
{tab.locked && <Lock size={10} className="text-gray-600 ml-0.5 flex-shrink-0" />}

// 新：
{tab.locked && <Lock size={10} className="ml-0.5 flex-shrink-0" />}
```

移除 `text-gray-600`，Lock icon 繼承父層 tab 文字色：inactive `text-gray-500`、hover `text-gray-300`、active `text-white`。

此為純 CSS 視覺調整，不加單元測試（視覺行為由瀏覽器驗證）。

- [ ] **Step 2: 跑全部測試 + build 驗證**

Run: `cd spa && npx vitest run && npm run build`
Expected: ALL PASS, build clean

- [ ] **Step 3: Commit**

```
git add spa/src/components/SortableTab.tsx
git commit -m "fix(tab-ui): lock icon inherits tab text color instead of fixed gray-600"
```
