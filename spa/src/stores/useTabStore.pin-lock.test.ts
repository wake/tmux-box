import { describe, it, expect, beforeEach } from 'vitest'
import { useTabStore, migrateTabStore } from './useTabStore'
import { createSessionTab } from '../types/tab'

function addTab(name: string) {
  const tab = createSessionTab({ label: name, hostId: 'local', sessionName: name })
  useTabStore.getState().addTab(tab)
  return tab
}

function reset() {
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, dismissedSessions: [] })
}

describe('pinTab / unpinTab', () => {
  beforeEach(reset)

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

  it('pinTab preserves existing locked=true', () => {
    const a = addTab('a')
    useTabStore.getState().lockTab(a.id)
    useTabStore.getState().pinTab(a.id)
    expect(useTabStore.getState().tabs[a.id].pinned).toBe(true)
    expect(useTabStore.getState().tabs[a.id].locked).toBe(true)
  })

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

  it('pinTab is no-op for nonexistent tab', () => {
    useTabStore.getState().pinTab('nonexistent')
    expect(useTabStore.getState().tabOrder).toHaveLength(0)
  })

  it('unpinTab is no-op for non-pinned tab', () => {
    const a = addTab('a')
    useTabStore.getState().unpinTab(a.id)
    expect(useTabStore.getState().tabs[a.id].pinned).toBe(false)
  })

  it('pinTab on already-pinned tab is no-op (preserves position)', () => {
    const a = addTab('a')
    const b = addTab('b')
    addTab('c') // third tab to verify ordering preserved
    useTabStore.getState().pinTab(a.id)
    useTabStore.getState().pinTab(b.id)
    const orderBefore = [...useTabStore.getState().tabOrder]
    useTabStore.getState().pinTab(a.id) // already pinned — should be no-op
    expect(useTabStore.getState().tabOrder).toEqual(orderBefore)
  })
})

describe('lockTab / unlockTab', () => {
  beforeEach(reset)

  it('lockTab sets locked=true', () => {
    const a = addTab('a')
    useTabStore.getState().lockTab(a.id)
    expect(useTabStore.getState().tabs[a.id].locked).toBe(true)
  })

  it('unlockTab sets locked=false', () => {
    const a = addTab('a')
    useTabStore.getState().lockTab(a.id)
    useTabStore.getState().unlockTab(a.id)
    expect(useTabStore.getState().tabs[a.id].locked).toBe(false)
  })

  it('unlockTab on pinned tab sets locked=false', () => {
    const a = addTab('a')
    useTabStore.getState().lockTab(a.id)
    useTabStore.getState().pinTab(a.id)
    useTabStore.getState().unlockTab(a.id)
    expect(useTabStore.getState().tabs[a.id].locked).toBe(false)
    expect(useTabStore.getState().tabs[a.id].pinned).toBe(true)
  })
})

describe('locked tab blocks close', () => {
  beforeEach(reset)

  it('removeTab on locked tab is no-op', () => {
    const a = addTab('a')
    useTabStore.getState().lockTab(a.id)
    useTabStore.getState().removeTab(a.id)
    expect(useTabStore.getState().tabs[a.id]).toBeDefined()
  })

  it('dismissTab on locked tab is no-op', () => {
    const a = addTab('a')
    useTabStore.getState().lockTab(a.id)
    useTabStore.getState().dismissTab(a.id)
    expect(useTabStore.getState().tabs[a.id]).toBeDefined()
  })

  it('removeTab on unlocked tab still works', () => {
    const a = addTab('a')
    useTabStore.getState().removeTab(a.id)
    expect(useTabStore.getState().tabs[a.id]).toBeUndefined()
  })

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
})

describe('updateTab invariant protection', () => {
  beforeEach(reset)

  it('updateTab ignores pinned field', () => {
    const a = addTab('a')
    useTabStore.getState().updateTab(a.id, { pinned: true } as any)
    expect(useTabStore.getState().tabs[a.id].pinned).toBe(false)
  })

  it('updateTab ignores locked field', () => {
    const a = addTab('a')
    useTabStore.getState().lockTab(a.id) // 明確 lock（pinTab 不再自動 lock）
    useTabStore.getState().pinTab(a.id)
    useTabStore.getState().updateTab(a.id, { locked: false } as any)
    expect(useTabStore.getState().tabs[a.id].locked).toBe(true)
  })
})

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

describe('persist migration v1→v2', () => {
  it('adds pinned=false, locked=false to old tabs', () => {
    const old = {
      tabs: {
        t1: { id: 't1', type: 'session', label: 'x', icon: 'Terminal', hostId: 'local', data: {} },
      },
      tabOrder: ['t1'],
      activeTabId: 't1',
      dismissedSessions: [],
    }
    const result = migrateTabStore(old, 1) as any
    expect(result.tabs.t1.pinned).toBe(false)
    expect(result.tabs.t1.locked).toBe(false)
  })

  it('preserves existing pinned/locked values', () => {
    const old = {
      tabs: {
        t1: { id: 't1', type: 'session', label: 'x', icon: 'Terminal', hostId: 'local', data: {}, pinned: true, locked: true },
      },
      tabOrder: ['t1'],
      activeTabId: 't1',
      dismissedSessions: [],
    }
    const result = migrateTabStore(old, 1) as any
    expect(result.tabs.t1.pinned).toBe(true)
    expect(result.tabs.t1.locked).toBe(true)
  })
})
