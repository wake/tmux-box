import { describe, it, expect, beforeEach } from 'vitest'
import { useTabStore } from './useTabStore'
import { createTab } from '../types/tab'
import { getPrimaryPane } from '../lib/pane-tree'

function makePane(tmuxInstance: string, sessionCode = 'abc123') {
  return createTab({
    kind: 'tmux-session', hostId: 'h1', sessionCode,
    mode: 'terminal', cachedName: 'dev', tmuxInstance,
  })
}

function seedPane(tmuxInstance: string, sessionCode = 'abc123') {
  const tab = makePane(tmuxInstance, sessionCode)
  useTabStore.setState({ tabs: { [tab.id]: tab }, tabOrder: [tab.id], activeTabId: tab.id })
  return tab
}

function terminatedOf(tabId: string) {
  const content = getPrimaryPane(useTabStore.getState().tabs[tabId].layout).content
  return content.kind === 'tmux-session' ? content.terminated : undefined
}

function instanceOf(tabId: string) {
  const content = getPrimaryPane(useTabStore.getState().tabs[tabId].layout).content
  return content.kind === 'tmux-session' ? content.tmuxInstance : undefined
}

describe('markTerminatedForGeneration', () => {
  beforeEach(() => useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null }))

  it('marks a pane whose recorded generation differs', () => {
    const tab = seedPane('111:1000')
    useTabStore.getState().markTerminatedForGeneration('h1', 'abc123', '111:1000', 'tmux-restarted')
    expect(terminatedOf(tab.id)).toBe('tmux-restarted')
  })

  it('leaves a sibling pane already bound to the new generation alone', () => {
    const stale = seedPane('111:1000')
    const fresh = makePane('222:2000')
    useTabStore.setState((s) => ({ tabs: { ...s.tabs, [fresh.id]: fresh }, tabOrder: [...s.tabOrder, fresh.id] }))

    useTabStore.getState().markTerminatedForGeneration('h1', 'abc123', '111:1000', 'tmux-restarted')

    expect(terminatedOf(fresh.id)).toBeUndefined()
    expect(terminatedOf(stale.id)).toBe('tmux-restarted')
  })

  it('matches a pane with no recorded generation by the old host+code rule', () => {
    const legacy = seedPane('')
    useTabStore.getState().markTerminatedForGeneration('h1', 'abc123', '111:1000', 'session-closed')
    expect(terminatedOf(legacy.id)).toBe('session-closed')
  })

  it('ignores other hosts and other session codes', () => {
    const other = seedPane('111:1000', 'zzz999')
    useTabStore.getState().markTerminatedForGeneration('h1', 'abc123', '111:1000', 'tmux-restarted')
    expect(terminatedOf(other.id)).toBeUndefined()
    useTabStore.getState().markTerminatedForGeneration('h2', 'zzz999', '111:1000', 'tmux-restarted')
    expect(terminatedOf(other.id)).toBeUndefined()
  })

  it('does not re-mark an already terminated pane', () => {
    const tab = seedPane('111:1000')
    useTabStore.getState().markTerminatedForGeneration('h1', 'abc123', '111:1000', 'session-closed')
    const before = useTabStore.getState().tabs[tab.id]
    useTabStore.getState().markTerminatedForGeneration('h1', 'abc123', '111:1000', 'tmux-restarted')
    expect(useTabStore.getState().tabs[tab.id]).toBe(before)
    expect(terminatedOf(tab.id)).toBe('session-closed')
  })
})

describe('adoptTmuxInstance', () => {
  beforeEach(() => useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null }))

  it('stamps the live generation onto a pane that has none', () => {
    const tab = seedPane('')
    useTabStore.getState().adoptTmuxInstance('h1', 'abc123', '222:2000')
    expect(instanceOf(tab.id)).toBe('222:2000')
  })

  it('never overwrites a generation the pane already carries', () => {
    const tab = seedPane('111:1000')
    const before = useTabStore.getState().tabs[tab.id]
    useTabStore.getState().adoptTmuxInstance('h1', 'abc123', '222:2000')
    expect(useTabStore.getState().tabs[tab.id]).toBe(before)
    expect(instanceOf(tab.id)).toBe('111:1000')
  })

  it('ignores an empty live generation', () => {
    const tab = seedPane('')
    useTabStore.getState().adoptTmuxInstance('h1', 'abc123', '')
    expect(instanceOf(tab.id)).toBe('')
  })

  it('ignores panes on another host or another code', () => {
    const tab = seedPane('', 'zzz999')
    useTabStore.getState().adoptTmuxInstance('h1', 'abc123', '222:2000')
    useTabStore.getState().adoptTmuxInstance('h2', 'zzz999', '222:2000')
    expect(instanceOf(tab.id)).toBe('')
  })
})
