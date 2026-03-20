import { describe, it, expect, beforeEach } from 'vitest'
import { useTabStore } from './useTabStore'
import { createTab } from '../types/tab'

describe('useTabStore', () => {
  beforeEach(() => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, dismissedSessions: [] })
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

  it('ignores setActiveTab with nonexistent id', () => {
    const tab = createTab({ type: 'terminal', label: 'dev', hostId: 'mlab' })
    useTabStore.getState().addTab(tab)
    useTabStore.getState().setActiveTab('nonexistent')
    expect(useTabStore.getState().activeTabId).toBe(tab.id)
  })

  it('ignores updateTab with nonexistent id', () => {
    useTabStore.getState().updateTab('nonexistent', { label: 'ghost' })
    expect(Object.keys(useTabStore.getState().tabs)).toHaveLength(0)
  })

  it('removeTab is no-op for nonexistent id', () => {
    const tab = createTab({ type: 'terminal', label: 'dev', hostId: 'mlab' })
    useTabStore.getState().addTab(tab)
    useTabStore.getState().removeTab('nonexistent')
    expect(useTabStore.getState().tabOrder).toHaveLength(1)
  })

  it('dismissTab adds sessionName to dismissedSessions', () => {
    const tab = createTab({ type: 'terminal', label: 'dev', hostId: 'mlab', sessionName: 'dev' })
    useTabStore.getState().addTab(tab)
    useTabStore.getState().dismissTab(tab.id)
    expect(useTabStore.getState().tabs[tab.id]).toBeUndefined()
    expect(useTabStore.getState().dismissedSessions).toContain('dev')
  })

  it('dismissTab is no-op for tab without sessionName', () => {
    const tab = createTab({ type: 'editor', label: 'file.ts', hostId: 'mlab', filePath: '/file.ts' })
    useTabStore.getState().addTab(tab)
    useTabStore.getState().dismissTab(tab.id)
    expect(useTabStore.getState().tabs[tab.id]).toBeUndefined()
    expect(useTabStore.getState().dismissedSessions).toHaveLength(0)
  })

  it('undismissSession removes sessionName from dismissedSessions', () => {
    const tab = createTab({ type: 'terminal', label: 'dev', hostId: 'mlab', sessionName: 'dev' })
    useTabStore.getState().addTab(tab)
    useTabStore.getState().dismissTab(tab.id)
    expect(useTabStore.getState().dismissedSessions).toContain('dev')
    useTabStore.getState().undismissSession('dev')
    expect(useTabStore.getState().dismissedSessions).not.toContain('dev')
  })

  it('isSessionDismissed returns correct value', () => {
    const tab = createTab({ type: 'terminal', label: 'dev', hostId: 'mlab', sessionName: 'dev' })
    useTabStore.getState().addTab(tab)
    expect(useTabStore.getState().isSessionDismissed('dev')).toBe(false)
    useTabStore.getState().dismissTab(tab.id)
    expect(useTabStore.getState().isSessionDismissed('dev')).toBe(true)
  })
})
