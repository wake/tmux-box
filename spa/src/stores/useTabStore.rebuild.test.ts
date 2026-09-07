// spa/src/stores/useTabStore.rebuild.test.ts — the per-pane rebuild record and
// its writer ranking (spec §4.1 / §4.4 / §4.5).
import { describe, it, expect, beforeEach } from 'vitest'
import { useTabStore } from './useTabStore'
import { createTab } from '../types/tab'
import type { Tab } from '../types/tab'
import { getPrimaryPane, findPane } from '../lib/pane-tree'

function seed(tmuxInstance = '111:1000') {
  const tab = createTab({
    kind: 'tmux-session', hostId: 'h1', sessionCode: 'abc123',
    mode: 'terminal', cachedName: 'dev', tmuxInstance,
  })
  useTabStore.setState({ tabs: { [tab.id]: tab }, tabOrder: [tab.id], activeTabId: tab.id })
  return tab
}

const rec = (tabId: string) => {
  const l = useTabStore.getState().tabs[tabId].layout
  return l.type === 'leaf' && l.pane.content.kind === 'tmux-session' ? l.pane.content.rebuild : undefined
}

/** Split `tab` in two panes bound to the same (host, code, generation). */
function splitTabWithSecondPane(tab: Tab, secondPaneId: string): Tab {
  const first = getPrimaryPane(tab.layout)
  return {
    ...tab,
    layout: {
      type: 'split',
      id: 'split-1',
      direction: 'h',
      sizes: [50, 50],
      children: [
        { type: 'leaf', pane: { ...first, id: 'p1' } },
        { type: 'leaf', pane: { id: secondPaneId, content: { ...first.content } } },
      ],
    },
  }
}

function paneContentOf(tabId: string, paneId: string) {
  const tab = useTabStore.getState().tabs[tabId]
  const pane = findPane(tab.layout, paneId)
  return pane && pane.content.kind === 'tmux-session' ? pane.content : undefined
}

const recordOfPane = (tabId: string, paneId: string) => paneContentOf(tabId, paneId)?.rebuild
const cachedNameOfPane = (tabId: string, paneId: string) => paneContentOf(tabId, paneId)?.cachedName

describe('setPaneRebuild', () => {
  beforeEach(() => useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null }))

  it('writes the agent group as a unit', () => {
    const tab = seed()
    useTabStore.getState().setPaneRebuild('h1', 'abc123', '111:1000', {
      kind: 'agent-group',
      record: {
        tmuxInstance: '111:1000', cwd: '/w/p', cwdSource: 'agent-session-start',
        agent: { type: 'codex', sessionId: 'S1', tmuxPaneId: '%2', updatedAt: 5 },
        resumeCommand: 'codex resume S1', capturedAt: 5,
      },
    })
    expect(rec(tab.id)?.agent?.sessionId).toBe('S1')
    expect(rec(tab.id)?.agent?.tmuxPaneId).toBe('%2')
    expect(rec(tab.id)?.cwd).toBe('/w/p')
    expect(rec(tab.id)?.sessionName).toBe('dev')
    expect(rec(tab.id)?.tmuxInstance).toBe('111:1000')
  })

  it('clears cwd when a later agent group has none', () => {
    const tab = seed()
    const store = useTabStore.getState()
    store.setPaneRebuild('h1', 'abc123', '111:1000', {
      kind: 'agent-group',
      record: {
        tmuxInstance: '111:1000', cwd: '/w/p', cwdSource: 'agent-session-start',
        agent: { type: 'codex', sessionId: 'S1', updatedAt: 5 }, resumeCommand: 'codex resume S1', capturedAt: 5,
      },
    })
    store.setPaneRebuild('h1', 'abc123', '111:1000', {
      kind: 'agent-group',
      record: {
        tmuxInstance: '111:1000', agent: { type: 'codex', sessionId: 'S2', updatedAt: 6 },
        resumeCommand: 'codex resume S2', capturedAt: 6,
      },
    })
    expect(rec(tab.id)?.agent?.sessionId).toBe('S2')
    expect(rec(tab.id)?.cwd).toBeUndefined()
    expect(rec(tab.id)?.cwdSource).toBeUndefined()
  })

  it('ignores a write for a different generation', () => {
    const tab = seed('111:1000')
    useTabStore.getState().setPaneRebuild('h1', 'abc123', '222:2000', {
      kind: 'field', field: 'cwd', value: '/other',
    })
    expect(rec(tab.id)?.cwd).toBeUndefined()
  })

  it('writes a pane whose recorded instance is empty (legacy pane)', () => {
    const tab = seed('')
    useTabStore.getState().setPaneRebuild('h1', 'abc123', '222:2000', {
      kind: 'field', field: 'cwd', value: '/legacy',
    })
    expect(rec(tab.id)?.cwd).toBe('/legacy')
  })

  it('probe cwd fills only when unset and never overwrites an agent cwd', () => {
    const tab = seed()
    const store = useTabStore.getState()
    store.setPaneRebuild('h1', 'abc123', '111:1000', { kind: 'probe-cwd', cwd: '/probe' })
    expect(rec(tab.id)?.cwd).toBe('/probe')
    expect(rec(tab.id)?.cwdSource).toBe('pane-probe')

    store.setPaneRebuild('h1', 'abc123', '111:1000', {
      kind: 'agent-group',
      record: {
        tmuxInstance: '111:1000', cwd: '/agent', cwdSource: 'agent-session-start',
        agent: { type: 'cc', updatedAt: 7 }, resumeCommand: 'claude -c', capturedAt: 7,
      },
    })
    store.setPaneRebuild('h1', 'abc123', '111:1000', { kind: 'probe-cwd', cwd: '/probe2' })
    expect(rec(tab.id)?.cwd).toBe('/agent')
    expect(rec(tab.id)?.cwdSource).toBe('agent-session-start')
  })

  it('a per-pane edit does not touch a split sibling on the same session', () => {
    const tab = createTab({
      kind: 'tmux-session', hostId: 'h1', sessionCode: 'abc123',
      mode: 'terminal', cachedName: 'dev', tmuxInstance: '111:1000',
    })
    const split = splitTabWithSecondPane(tab, 'p2')   // same (host, code, generation)
    useTabStore.setState({ tabs: { [split.id]: split }, tabOrder: [split.id], activeTabId: split.id })

    useTabStore.getState().setPaneRebuildForPane(split.id, 'p2',
      { hostId: 'h1', sessionCode: 'abc123', tmuxInstance: '111:1000' },
      { kind: 'field', field: 'cwd', value: '/only-p2' })

    expect(recordOfPane(split.id, 'p2')?.cwd).toBe('/only-p2')
    expect(recordOfPane(split.id, 'p1')?.cwd).toBeUndefined()
  })

  it('a per-pane edit for a different generation is ignored', () => {
    const tab = seed('111:1000')
    useTabStore.getState().setPaneRebuildForPane(tab.id, getPrimaryPane(tab.layout).id,
      { hostId: 'h1', sessionCode: 'abc123', tmuxInstance: '222:2000' },
      { kind: 'field', field: 'cwd', value: '/other' })
    expect(rec(tab.id)?.cwd).toBeUndefined()
  })

  it('a field edit touches only that field', () => {
    const tab = seed()
    const store = useTabStore.getState()
    store.setPaneRebuild('h1', 'abc123', '111:1000', {
      kind: 'agent-group',
      record: {
        tmuxInstance: '111:1000', cwd: '/w/p', cwdSource: 'agent-session-start',
        agent: { type: 'cc', sessionId: 'S1', updatedAt: 5 }, resumeCommand: 'claude --resume S1', capturedAt: 5,
      },
    })
    store.setPaneRebuild('h1', 'abc123', '111:1000', { kind: 'field', field: 'cwd', value: '/edited' })
    expect(rec(tab.id)?.cwd).toBe('/edited')
    expect(rec(tab.id)?.resumeCommand).toBe('claude --resume S1')
    expect(rec(tab.id)?.agent?.sessionId).toBe('S1')
  })

  it('stamps capturedAt on every write', () => {
    const tab = seed()
    const before = Date.now()
    useTabStore.getState().setPaneRebuild('h1', 'abc123', '111:1000', {
      kind: 'agent-group',
      record: {
        tmuxInstance: '111:1000', agent: { type: 'cc', updatedAt: 1 },
        resumeCommand: 'claude -c', capturedAt: 1,   // stale stamp from the payload
      },
    })
    expect(rec(tab.id)!.capturedAt).toBeGreaterThanOrEqual(before)
  })

  it('a fresh agent group clears a stale unverified flag', () => {
    const tab = seed()
    const store = useTabStore.getState()
    store.setPaneRebuild('h1', 'abc123', '111:1000', { kind: 'unverified', unverified: true })
    expect(rec(tab.id)?.unverified).toBe(true)
    store.setPaneRebuild('h1', 'abc123', '111:1000', {
      kind: 'agent-group',
      record: {
        tmuxInstance: '111:1000', agent: { type: 'cc', sessionId: 'S9', updatedAt: 9 },
        resumeCommand: 'claude --resume S9', capturedAt: 9,
      },
    })
    expect(rec(tab.id)?.unverified).toBeUndefined()
  })

  it('survives a view-mode round trip', () => {
    const tab = seed()
    const paneId = getPrimaryPane(tab.layout).id
    const store = useTabStore.getState()
    store.setPaneRebuild('h1', 'abc123', '111:1000', { kind: 'field', field: 'cwd', value: '/w/p' })
    store.setViewMode(tab.id, paneId, 'stream')
    store.setViewMode(tab.id, paneId, 'terminal')
    expect(rec(tab.id)?.cwd).toBe('/w/p')
  })

  it('skips a pane that terminated while the write was in flight', () => {
    // The probe asks about a binding, not a pane. If pane A dies while the
    // answer is in flight and its sibling B is still on the same reused code,
    // the answer describes B's session — writing it into A's now-historical
    // record would put the wrong directory into the rebuild it offers.
    const tab = seed()
    const split = splitTabWithSecondPane(tab, 'p2')
    if (split.layout.type !== 'split') throw new Error('bad fixture')
    const [first, second] = split.layout.children
    if (second.type !== 'leaf' || second.pane.content.kind !== 'tmux-session') throw new Error('bad fixture')
    const dead = {
      ...second,
      pane: { ...second.pane, content: { ...second.pane.content, terminated: 'session-closed' as const } },
    }
    useTabStore.setState({
      tabs: { [tab.id]: { ...split, layout: { ...split.layout, children: [first, dead] } } },
      tabOrder: [tab.id],
      activeTabId: tab.id,
    })

    useTabStore.getState().setPaneRebuild('h1', 'abc123', '111:1000', { kind: 'probe-cwd', cwd: '/w/answer' })

    expect(recordOfPane(tab.id, 'p1')?.cwd).toBe('/w/answer')
    expect(recordOfPane(tab.id, 'p2')).toBeUndefined()
  })

  it('leaves stream-mode panes alone', () => {
    const tab = createTab({
      kind: 'tmux-session', hostId: 'h1', sessionCode: 'abc123',
      mode: 'stream', cachedName: 'dev', tmuxInstance: '111:1000',
    })
    useTabStore.setState({ tabs: { [tab.id]: tab }, tabOrder: [tab.id], activeTabId: tab.id })
    useTabStore.getState().setPaneRebuild('h1', 'abc123', '111:1000', {
      kind: 'field', field: 'cwd', value: '/nope',
    })
    expect(rec(tab.id)).toBeUndefined()
  })
})

describe('updateSessionCache — generation scoped', () => {
  beforeEach(() => useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null }))

  it('a rename follows into the rebuild record', () => {
    const tab = seed()
    const store = useTabStore.getState()
    store.setPaneRebuild('h1', 'abc123', '111:1000', { kind: 'field', field: 'cwd', value: '/w' })
    store.updateSessionCache('h1', 'abc123', 'renamed', '111:1000')
    expect(rec(tab.id)?.sessionName).toBe('renamed')
  })

  it('a rename from a different generation is ignored', () => {
    const tab = seed('111:1000')
    useTabStore.getState().updateSessionCache('h1', 'abc123', 'stranger', '222:2000')
    const l = useTabStore.getState().tabs[tab.id].layout
    expect(l.type === 'leaf' && l.pane.content.kind === 'tmux-session'
      && l.pane.content.cachedName).toBe('dev')
  })

  it('renames a secondary split pane, not just the primary', () => {
    const split = splitTabWithSecondPane(
      createTab({
        kind: 'tmux-session', hostId: 'h1', sessionCode: 'abc123',
        mode: 'terminal', cachedName: 'dev', tmuxInstance: '111:1000',
      }), 'p2')
    useTabStore.setState({ tabs: { [split.id]: split }, tabOrder: [split.id], activeTabId: split.id })
    useTabStore.getState().updateSessionCache('h1', 'abc123', 'renamed', '111:1000')
    expect(cachedNameOfPane(split.id, 'p2')).toBe('renamed')
    expect(cachedNameOfPane(split.id, 'p1')).toBe('renamed')
  })
})
