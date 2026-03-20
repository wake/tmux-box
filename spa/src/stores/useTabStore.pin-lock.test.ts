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

  it('pinTab sets pinned=true, locked=true, moves to pinned zone end', () => {
    const a = addTab('a')
    const b = addTab('b')
    const c = addTab('c')
    useTabStore.getState().pinTab(b.id)

    const state = useTabStore.getState()
    expect(state.tabs[b.id].pinned).toBe(true)
    expect(state.tabs[b.id].locked).toBe(true)
    expect(state.tabOrder).toEqual([b.id, a.id, c.id])
  })

  it('unpinTab sets pinned=false, moves to normal zone start, keeps locked', () => {
    const a = addTab('a')
    addTab('b') // second tab to verify zone ordering
    useTabStore.getState().pinTab(a.id)
    useTabStore.getState().unpinTab(a.id)

    const state = useTabStore.getState()
    expect(state.tabs[a.id].pinned).toBe(false)
    expect(state.tabs[a.id].locked).toBe(true)
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
    const c = addTab('c')
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

  it('unlockTab on pinned tab is no-op', () => {
    const a = addTab('a')
    useTabStore.getState().pinTab(a.id)
    useTabStore.getState().unlockTab(a.id)
    expect(useTabStore.getState().tabs[a.id].locked).toBe(true)
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
    useTabStore.getState().pinTab(a.id)
    useTabStore.getState().updateTab(a.id, { locked: false } as any)
    expect(useTabStore.getState().tabs[a.id].locked).toBe(true)
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
