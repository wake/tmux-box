import { describe, it, expect, beforeEach } from 'vitest'
import { useTabStore } from './useTabStore'
import { createSessionTab, createEditorTab } from '../types/tab'

describe('useTabStore', () => {
  beforeEach(() => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, dismissedSessions: [] })
  })

  it('adds a tab', () => {
    const tab = createSessionTab({ label: 'dev', hostId: 'mlab', sessionName: 'dev' })
    useTabStore.getState().addTab(tab)
    const state = useTabStore.getState()
    expect(state.tabs[tab.id]).toEqual(tab)
    expect(state.tabOrder).toContain(tab.id)
  })

  it('sets active tab on add if none active', () => {
    const tab = createSessionTab({ label: 'dev', hostId: 'mlab', sessionName: 'dev' })
    useTabStore.getState().addTab(tab)
    expect(useTabStore.getState().activeTabId).toBe(tab.id)
  })

  it('does not change active tab when adding second tab', () => {
    const tab1 = createSessionTab({ label: 'dev', hostId: 'mlab', sessionName: 'dev' })
    const tab2 = createSessionTab({ label: 'claude', hostId: 'mlab', sessionName: 'claude', viewMode: 'stream' })
    useTabStore.getState().addTab(tab1)
    useTabStore.getState().addTab(tab2)
    expect(useTabStore.getState().activeTabId).toBe(tab1.id)
  })

  it('removes a tab', () => {
    const tab = createSessionTab({ label: 'dev', hostId: 'mlab', sessionName: 'dev' })
    useTabStore.getState().addTab(tab)
    useTabStore.getState().removeTab(tab.id)
    expect(useTabStore.getState().tabs[tab.id]).toBeUndefined()
    expect(useTabStore.getState().tabOrder).not.toContain(tab.id)
  })

  it('activates next tab when removing active tab', () => {
    const tab1 = createSessionTab({ label: 'dev', hostId: 'mlab', sessionName: 'dev' })
    const tab2 = createSessionTab({ label: 'claude', hostId: 'mlab', sessionName: 'claude', viewMode: 'stream' })
    useTabStore.getState().addTab(tab1)
    useTabStore.getState().addTab(tab2)
    useTabStore.getState().setActiveTab(tab1.id)
    useTabStore.getState().removeTab(tab1.id)
    expect(useTabStore.getState().activeTabId).toBe(tab2.id)
  })

  it('sets activeTabId to null when removing last tab', () => {
    const tab = createSessionTab({ label: 'dev', hostId: 'mlab', sessionName: 'dev' })
    useTabStore.getState().addTab(tab)
    useTabStore.getState().removeTab(tab.id)
    expect(useTabStore.getState().activeTabId).toBeNull()
  })

  it('switches active tab', () => {
    const tab1 = createSessionTab({ label: 'dev', hostId: 'mlab', sessionName: 'dev' })
    const tab2 = createSessionTab({ label: 'claude', hostId: 'mlab', sessionName: 'claude', viewMode: 'stream' })
    useTabStore.getState().addTab(tab1)
    useTabStore.getState().addTab(tab2)
    useTabStore.getState().setActiveTab(tab2.id)
    expect(useTabStore.getState().activeTabId).toBe(tab2.id)
  })

  it('reorders tabs', () => {
    const tab1 = createSessionTab({ label: 'a', hostId: 'mlab', sessionName: 'a' })
    const tab2 = createSessionTab({ label: 'b', hostId: 'mlab', sessionName: 'b' })
    const tab3 = createSessionTab({ label: 'c', hostId: 'mlab', sessionName: 'c' })
    useTabStore.getState().addTab(tab1)
    useTabStore.getState().addTab(tab2)
    useTabStore.getState().addTab(tab3)
    useTabStore.getState().reorderTabs([tab3.id, tab1.id, tab2.id])
    expect(useTabStore.getState().tabOrder).toEqual([tab3.id, tab1.id, tab2.id])
  })

  it('updates tab properties', () => {
    const tab = createEditorTab({ label: 'file.ts', hostId: 'mlab', filePath: '/file.ts' })
    useTabStore.getState().addTab(tab)
    useTabStore.getState().updateTab(tab.id, { data: { ...tab.data, isDirty: true }, label: 'file.ts *' })
    expect(useTabStore.getState().tabs[tab.id].data.isDirty).toBe(true)
    expect(useTabStore.getState().tabs[tab.id].label).toBe('file.ts *')
  })

  it('returns active tab via getActiveTab', () => {
    const tab = createSessionTab({ label: 'dev', hostId: 'mlab', sessionName: 'dev' })
    useTabStore.getState().addTab(tab)
    expect(useTabStore.getState().getActiveTab()).toEqual(tab)
  })

  it('ignores setActiveTab with nonexistent id', () => {
    const tab = createSessionTab({ label: 'dev', hostId: 'mlab', sessionName: 'dev' })
    useTabStore.getState().addTab(tab)
    useTabStore.getState().setActiveTab('nonexistent')
    expect(useTabStore.getState().activeTabId).toBe(tab.id)
  })

  it('ignores updateTab with nonexistent id', () => {
    useTabStore.getState().updateTab('nonexistent', { label: 'ghost' })
    expect(Object.keys(useTabStore.getState().tabs)).toHaveLength(0)
  })

  it('removeTab is no-op for nonexistent id', () => {
    const tab = createSessionTab({ label: 'dev', hostId: 'mlab', sessionName: 'dev' })
    useTabStore.getState().addTab(tab)
    useTabStore.getState().removeTab('nonexistent')
    expect(useTabStore.getState().tabOrder).toHaveLength(1)
  })

  it('dismissTab adds sessionName to dismissedSessions', () => {
    const tab = createSessionTab({ label: 'dev', hostId: 'mlab', sessionName: 'dev' })
    useTabStore.getState().addTab(tab)
    useTabStore.getState().dismissTab(tab.id)
    expect(useTabStore.getState().tabs[tab.id]).toBeUndefined()
    expect(useTabStore.getState().dismissedSessions).toContain('dev')
  })

  it('dismissTab is no-op for tab without sessionName', () => {
    const tab = createEditorTab({ label: 'file.ts', hostId: 'mlab', filePath: '/file.ts' })
    useTabStore.getState().addTab(tab)
    useTabStore.getState().dismissTab(tab.id)
    expect(useTabStore.getState().tabs[tab.id]).toBeUndefined()
    expect(useTabStore.getState().dismissedSessions).toHaveLength(0)
  })

  it('undismissSession removes sessionName from dismissedSessions', () => {
    const tab = createSessionTab({ label: 'dev', hostId: 'mlab', sessionName: 'dev' })
    useTabStore.getState().addTab(tab)
    useTabStore.getState().dismissTab(tab.id)
    expect(useTabStore.getState().dismissedSessions).toContain('dev')
    useTabStore.getState().undismissSession('dev')
    expect(useTabStore.getState().dismissedSessions).not.toContain('dev')
  })

  it('isSessionDismissed returns correct value', () => {
    const tab = createSessionTab({ label: 'dev', hostId: 'mlab', sessionName: 'dev' })
    useTabStore.getState().addTab(tab)
    expect(useTabStore.getState().isSessionDismissed('dev')).toBe(false)
    useTabStore.getState().dismissTab(tab.id)
    expect(useTabStore.getState().isSessionDismissed('dev')).toBe(true)
  })

  it('setViewMode updates tab viewMode', () => {
    const tab = createSessionTab({ label: 'dev', hostId: 'mlab', sessionName: 'dev' })
    useTabStore.getState().addTab(tab)
    useTabStore.getState().setViewMode(tab.id, 'stream')
    expect(useTabStore.getState().tabs[tab.id].viewMode).toBe('stream')
  })

  it('setViewMode is no-op for nonexistent tab', () => {
    useTabStore.getState().setViewMode('nonexistent', 'stream')
    expect(Object.keys(useTabStore.getState().tabs)).toHaveLength(0)
  })
})

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
    const b = addTab('b')
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
