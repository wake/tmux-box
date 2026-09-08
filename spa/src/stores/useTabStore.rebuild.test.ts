// spa/src/stores/useTabStore.rebuild.test.ts — the per-pane rebuild record and
// its writer ranking (spec §4.1 / §4.4 / §4.5).
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { useTabStore } from './useTabStore'
import { createTab } from '../types/tab'
import type { PaneRebuildRecord, Tab } from '../types/tab'
import { getPrimaryPane, findPane, updatePaneInLayout } from '../lib/pane-tree'
import { batchCandidates, collectRecordRows } from '../lib/rebuild/eligibility'
import { groupForBatch } from '../lib/rebuild/batch'
import { resolveResumeCommand } from '../lib/rebuild/composer'
import { useResumeTemplateStore, type ResumeTemplateLookup } from './useResumeTemplateStore'

/** The shipped templates: the store answers from `DEFAULT_RESUME_TEMPLATES`. */
const defaultTemplates: ResumeTemplateLookup = (agentType) =>
  useResumeTemplateStore.getState().getTemplates(agentType)

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

/** Kill ONE pane of a split, the way a session that only that pane held would. */
function killPane(tabId: string, paneId: string) {
  const tab = useTabStore.getState().tabs[tabId]
  const content = paneContentOf(tabId, paneId)
  if (!content) throw new Error('bad fixture')
  useTabStore.setState((state) => ({
    tabs: {
      ...state.tabs,
      [tabId]: {
        ...tab,
        layout: updatePaneInLayout(tab.layout, paneId, { ...content, terminated: 'session-closed' }),
      },
    },
  }))
}

describe('setPaneRebuild', () => {
  beforeEach(() => useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null }))

  it('writes the agent group as a unit', () => {
    const tab = seed()
    useTabStore.getState().setPaneRebuild('h1', 'abc123', '111:1000', {
      kind: 'agent-group',
      record: {
        tmuxInstance: '111:1000', cwd: '/w/p', cwdSource: 'agent-session-start',
        agent: { type: 'codex', sessionId: 'S1', tmuxPaneId: '%2', updatedAt: 5 },
        capturedAt: 5,
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
        agent: { type: 'codex', sessionId: 'S1', updatedAt: 5 }, capturedAt: 5,
      },
    })
    store.setPaneRebuild('h1', 'abc123', '111:1000', {
      kind: 'agent-group',
      record: {
        tmuxInstance: '111:1000', agent: { type: 'codex', sessionId: 'S2', updatedAt: 6 },
        capturedAt: 6,
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
        agent: { type: 'cc', updatedAt: 7 }, capturedAt: 7,
      },
    })
    store.setPaneRebuild('h1', 'abc123', '111:1000', { kind: 'probe-cwd', cwd: '/probe2' })
    expect(rec(tab.id)?.cwd).toBe('/agent')
    expect(rec(tab.id)?.cwdSource).toBe('agent-session-start')
  })

  it('marks a hand-typed cwd as user-sourced', () => {
    const tab = seed()
    useTabStore.getState().setPaneRebuild('h1', 'abc123', '111:1000', {
      kind: 'field', field: 'cwd', value: '/typed',
    })
    expect(rec(tab.id)?.cwd).toBe('/typed')
    expect(rec(tab.id)?.cwdSource).toBe('user')
  })

  it('retyping the cwd a probe already found is a confirmation, not a no-op', () => {
    // The value does not change, but the PROVENANCE does: the user has now
    // approved this directory, and the agent backfill's fill mode must not
    // overwrite it afterwards. So the same-value early return must not fire.
    const tab = seed()
    const paneId = getPrimaryPane(tab.layout).id
    const store = useTabStore.getState()
    store.setPaneRebuild('h1', 'abc123', '111:1000', { kind: 'probe-cwd', cwd: '/probe' })
    expect(rec(tab.id)?.cwdSource).toBe('pane-probe')

    const before = paneContentOf(tab.id, paneId)
    store.setPaneRebuild('h1', 'abc123', '111:1000', { kind: 'field', field: 'cwd', value: '/probe' })
    expect(rec(tab.id)?.cwd).toBe('/probe')
    expect(rec(tab.id)?.cwdSource).toBe('user')
    expect(paneContentOf(tab.id, paneId)).not.toBe(before)
  })

  it('retyping a cwd that is already user-sourced is still a no-op', () => {
    const tab = seed()
    const paneId = getPrimaryPane(tab.layout).id
    const store = useTabStore.getState()
    store.setPaneRebuild('h1', 'abc123', '111:1000', { kind: 'field', field: 'cwd', value: '/typed' })

    const before = paneContentOf(tab.id, paneId)
    store.setPaneRebuild('h1', 'abc123', '111:1000', { kind: 'field', field: 'cwd', value: '/typed' })
    expect(paneContentOf(tab.id, paneId)).toBe(before)
  })

  it('a probe does not overwrite a user-typed cwd', () => {
    const tab = seed()
    const store = useTabStore.getState()
    store.setPaneRebuild('h1', 'abc123', '111:1000', { kind: 'field', field: 'cwd', value: '/typed' })
    store.setPaneRebuild('h1', 'abc123', '111:1000', { kind: 'probe-cwd', cwd: '/probe' })
    expect(rec(tab.id)?.cwd).toBe('/typed')
    expect(rec(tab.id)?.cwdSource).toBe('user')
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
        agent: { type: 'cc', sessionId: 'S1', updatedAt: 5 }, capturedAt: 5,
      },
    })
    store.setPaneRebuild('h1', 'abc123', '111:1000', {
      kind: 'field', field: 'resumeCommandOverride', value: 'cld-yolo --resume S1',
    })
    store.setPaneRebuild('h1', 'abc123', '111:1000', { kind: 'field', field: 'cwd', value: '/edited' })
    expect(rec(tab.id)?.cwd).toBe('/edited')
    expect(rec(tab.id)?.resumeCommandOverride).toBe('cld-yolo --resume S1')
    expect(rec(tab.id)?.agent?.sessionId).toBe('S1')
  })

  it('stamps capturedAt on every write', () => {
    const tab = seed()
    const before = Date.now()
    useTabStore.getState().setPaneRebuild('h1', 'abc123', '111:1000', {
      kind: 'agent-group',
      record: {
        tmuxInstance: '111:1000', agent: { type: 'cc', updatedAt: 1 },
        capturedAt: 1,   // stale stamp from the payload
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
        capturedAt: 9,
      },
    })
    expect(rec(tab.id)?.unverified).toBeUndefined()
  })

  // === The override's lifetime is scoped to the agent identity (spec §4.3) ===
  //
  // The one hazard that silently does the WRONG thing is a verbatim command
  // carrying a dead session id, and that is exactly an identity change. An
  // idle SessionStart re-emit is not, so it must not throw the edit away.
  describe('resumeCommandOverride and the agent group', () => {
    const seedOverride = (agent: NonNullable<PaneRebuildRecord['agent']>) => {
      const store = useTabStore.getState()
      store.setPaneRebuild('h1', 'abc123', '111:1000', {
        kind: 'agent-group',
        record: { tmuxInstance: '111:1000', cwd: '/w/p', agent, capturedAt: 1 },
      })
      store.setPaneRebuild('h1', 'abc123', '111:1000', {
        kind: 'field', field: 'resumeCommandOverride', value: 'cld-yolo -c',
      })
    }

    const group = (agent: NonNullable<PaneRebuildRecord['agent']>) =>
      useTabStore.getState().setPaneRebuild('h1', 'abc123', '111:1000', {
        kind: 'agent-group',
        record: { tmuxInstance: '111:1000', cwd: '/w/p', agent, capturedAt: 2 },
      })

    it('keeps the override when the same identity re-emits', () => {
      const tab = seed()
      seedOverride({ type: 'cc', sessionId: 'S1', updatedAt: 1 })
      group({ type: 'cc', sessionId: 'S1', tmuxPaneId: '%4', updatedAt: 2 })
      expect(rec(tab.id)?.resumeCommandOverride).toBe('cld-yolo -c')
    })

    it('clears the override when the session id changes', () => {
      const tab = seed()
      seedOverride({ type: 'cc', sessionId: 'S1', updatedAt: 1 })
      group({ type: 'cc', sessionId: 'S2', updatedAt: 2 })
      expect(rec(tab.id)?.resumeCommandOverride).toBeUndefined()
    })

    it('clears the override when the agent type changes', () => {
      const tab = seed()
      seedOverride({ type: 'cc', sessionId: 'S1', updatedAt: 1 })
      group({ type: 'codex', sessionId: 'S1', updatedAt: 2 })
      expect(rec(tab.id)?.resumeCommandOverride).toBeUndefined()
    })

    it('keeps an override typed before any agent was ever recorded', () => {
      // Nothing was invalidated: the override was not written against an
      // identity, so the first identity to arrive cannot have changed it. This
      // is the same rule the backfill's fill mode obeys.
      const tab = seed()
      useTabStore.getState().setPaneRebuild('h1', 'abc123', '111:1000', {
        kind: 'field', field: 'resumeCommandOverride', value: 'cld-yolo -c',
      })
      group({ type: 'cc', sessionId: 'S1', updatedAt: 2 })
      expect(rec(tab.id)?.resumeCommandOverride).toBe('cld-yolo -c')
      expect(rec(tab.id)?.agent?.sessionId).toBe('S1')
    })
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

// The `agent-backfill` patch (spec §5.5): the daemon's ownership answer, applied
// under four ORDERED, mutually exclusive modes — fill, replace, confirm, no-op.
// The answer never carries a command: the record holds an agent identity and
// the resolver composes from it, so what each mode decides about
// `resumeCommandOverride` is only whether the user's edit survives.
describe('setPaneRebuild — agent-backfill', () => {
  beforeEach(() => useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null }))

  const backfill = (record: {
    tmuxInstance: string
    agent: NonNullable<PaneRebuildRecord['agent']>
    cwd?: string
  }) => useTabStore.getState().setPaneRebuild('h1', 'abc123', '111:1000', { kind: 'agent-backfill', record })

  const seedAgentGroup = (record: Omit<PaneRebuildRecord, 'sessionName'>) =>
    useTabStore.getState().setPaneRebuild('h1', 'abc123', '111:1000', { kind: 'agent-group', record })

  const answer = { type: 'cc', sessionId: 'S1', tmuxPaneId: '%3', updatedAt: 42 }

  it('mode 1 (fill): an agent-less record takes the whole answer', () => {
    const tab = seed()
    backfill({ tmuxInstance: '111:1000', agent: answer, cwd: '/w/answer' })
    expect(rec(tab.id)?.agent).toEqual(answer)
    expect(rec(tab.id)?.cwd).toBe('/w/answer')
    expect(rec(tab.id)?.cwdSource).toBe('agent-backfill')
    // The identity is all the record needs; nothing is composed into it, and
    // no override is invented on the user's behalf.
    expect(rec(tab.id)?.resumeCommandOverride).toBeUndefined()
    expect(resolveResumeCommand(rec(tab.id), defaultTemplates)).toBe('claude --resume S1')
  })

  it('mode 1 (fill): a probe cwd is upgraded to the answer', () => {
    const tab = seed()
    useTabStore.getState().setPaneRebuild('h1', 'abc123', '111:1000', { kind: 'probe-cwd', cwd: '/probe' })
    backfill({ tmuxInstance: '111:1000', agent: answer, cwd: '/w/answer' })
    expect(rec(tab.id)?.cwd).toBe('/w/answer')
    expect(rec(tab.id)?.cwdSource).toBe('agent-backfill')
  })

  it('mode 1 (fill): a user-typed cwd is not overwritten', () => {
    const tab = seed()
    useTabStore.getState().setPaneRebuild('h1', 'abc123', '111:1000', {
      kind: 'field', field: 'cwd', value: '/typed',
    })
    backfill({ tmuxInstance: '111:1000', agent: answer, cwd: '/w/answer' })
    expect(rec(tab.id)?.cwd).toBe('/typed')
    expect(rec(tab.id)?.cwdSource).toBe('user')
    expect(rec(tab.id)?.agent).toEqual(answer)   // the agent still lands
  })

  it('mode 1 (fill): a SessionStart cwd is not overwritten', () => {
    // An agent-less record can still hold an 'agent-session-start' cwd — the
    // agent group and the directory are written together, and only the agent
    // half is guaranteed to have survived into a persisted record.
    const tab = seed()
    useTabStore.setState((state) => {
      const l = state.tabs[tab.id].layout
      if (l.type !== 'leaf' || l.pane.content.kind !== 'tmux-session') throw new Error('bad fixture')
      const content = {
        ...l.pane.content,
        rebuild: {
          sessionName: 'dev', tmuxInstance: '111:1000',
          cwd: '/w/start', cwdSource: 'agent-session-start' as const, capturedAt: 1,
        },
      }
      return { tabs: { [tab.id]: { ...state.tabs[tab.id], layout: { ...l, pane: { ...l.pane, content } } } } }
    })
    backfill({ tmuxInstance: '111:1000', agent: answer, cwd: '/w/answer' })
    expect(rec(tab.id)?.cwd).toBe('/w/start')
    expect(rec(tab.id)?.cwdSource).toBe('agent-session-start')
  })

  it('mode 1 (fill): an answer with no cwd leaves the recorded one alone', () => {
    const tab = seed()
    useTabStore.getState().setPaneRebuild('h1', 'abc123', '111:1000', { kind: 'probe-cwd', cwd: '/probe' })
    backfill({ tmuxInstance: '111:1000', agent: answer })
    expect(rec(tab.id)?.cwd).toBe('/probe')
    expect(rec(tab.id)?.cwdSource).toBe('pane-probe')
  })

  it('mode 1 (fill): a hand-typed override survives', () => {
    // Nothing was invalidated: the override predates any recorded identity, so
    // the first one to arrive cannot have made it stale (spec §4.3).
    const tab = seed()
    useTabStore.getState().setPaneRebuild('h1', 'abc123', '111:1000', {
      kind: 'field', field: 'resumeCommandOverride', value: 'cld-yolo -c',
    })
    backfill({ tmuxInstance: '111:1000', agent: answer, cwd: '/w/answer' })
    expect(rec(tab.id)?.resumeCommandOverride).toBe('cld-yolo -c')
    expect(resolveResumeCommand(rec(tab.id), defaultTemplates)).toBe('cld-yolo -c')
  })

  it('mode 2 (replace): an unverified record with a different agent type is replaced whole', () => {
    const tab = seed()
    seedAgentGroup({
      tmuxInstance: '111:1000', cwd: '/w/old', cwdSource: 'agent-session-start',
      agent: { type: 'codex', sessionId: 'OLD', updatedAt: 1 },
      capturedAt: 1,
    })
    useTabStore.getState().setPaneRebuild('h1', 'abc123', '111:1000', { kind: 'unverified', unverified: true })

    backfill({ tmuxInstance: '111:1000', agent: answer, cwd: '/w/answer' })
    expect(rec(tab.id)?.agent).toEqual(answer)
    expect(rec(tab.id)?.cwd).toBe('/w/answer')
    expect(rec(tab.id)?.cwdSource).toBe('agent-backfill')
    expect(resolveResumeCommand(rec(tab.id), defaultTemplates)).toBe('claude --resume S1')
    expect(rec(tab.id)?.unverified).toBeUndefined()
  })

  it('mode 2 (replace): a different session id of the same type also replaces', () => {
    const tab = seed()
    seedAgentGroup({
      tmuxInstance: '111:1000', cwd: '/w/old', cwdSource: 'agent-session-start',
      agent: { type: 'cc', sessionId: 'OLD', updatedAt: 1 },
      capturedAt: 1,
    })
    useTabStore.getState().setPaneRebuild('h1', 'abc123', '111:1000', { kind: 'unverified', unverified: true })

    backfill({ tmuxInstance: '111:1000', agent: answer })
    expect(rec(tab.id)?.agent?.sessionId).toBe('S1')
    expect(rec(tab.id)?.cwd).toBeUndefined()          // whole group, so the old cwd goes
    expect(rec(tab.id)?.cwdSource).toBeUndefined()
    expect(rec(tab.id)?.unverified).toBeUndefined()
  })

  it('mode 2 (replace): a user-typed cwd is the one thing kept', () => {
    const tab = seed()
    seedAgentGroup({
      tmuxInstance: '111:1000',
      agent: { type: 'codex', sessionId: 'OLD', updatedAt: 1 },
      capturedAt: 1,
    })
    const store = useTabStore.getState()
    store.setPaneRebuild('h1', 'abc123', '111:1000', { kind: 'field', field: 'cwd', value: '/typed' })
    store.setPaneRebuild('h1', 'abc123', '111:1000', { kind: 'unverified', unverified: true })

    backfill({ tmuxInstance: '111:1000', agent: answer, cwd: '/w/answer' })
    expect(rec(tab.id)?.cwd).toBe('/typed')
    expect(rec(tab.id)?.cwdSource).toBe('user')
    expect(rec(tab.id)?.agent).toEqual(answer)
  })

  it('mode 2 (replace): a hand-typed override does NOT survive', () => {
    // The identity it was written against is exactly what the correction
    // changed, so the command it names is the stale kind (spec §4.3).
    const tab = seed()
    seedAgentGroup({
      tmuxInstance: '111:1000',
      agent: { type: 'codex', sessionId: 'OLD', updatedAt: 1 },
      capturedAt: 1,
    })
    const store = useTabStore.getState()
    store.setPaneRebuild('h1', 'abc123', '111:1000', {
      kind: 'field', field: 'resumeCommandOverride', value: 'cld-yolo -c',
    })
    store.setPaneRebuild('h1', 'abc123', '111:1000', { kind: 'unverified', unverified: true })

    backfill({ tmuxInstance: '111:1000', agent: answer })
    expect(rec(tab.id)?.resumeCommandOverride).toBeUndefined()
    expect(resolveResumeCommand(rec(tab.id), defaultTemplates)).toBe('claude --resume S1')
  })

  it('mode 3 (confirm): an agreeing answer clears unverified and changes nothing else', () => {
    // This is what makes the probe TERMINATE: without it a record the daemon
    // agrees with stays flagged, stays eligible, and re-asks every 30 s forever.
    //
    // "Nothing else" INCLUDES `capturedAt`, so the whole record is compared
    // rather than the handful of fields a reader happens to remember.
    vi.useFakeTimers()
    try {
      vi.setSystemTime(1_000)
      const tab = seed()
      seedAgentGroup({
        tmuxInstance: '111:1000', cwd: '/w/old', cwdSource: 'agent-session-start',
        agent: { type: 'cc', sessionId: 'S1', tmuxPaneId: '%9', updatedAt: 1 },
        capturedAt: 1,
      })
      const store = useTabStore.getState()
      store.setPaneRebuild('h1', 'abc123', '111:1000', {
        kind: 'field', field: 'resumeCommandOverride', value: 'cld-yolo -c',
      })
      store.setPaneRebuild('h1', 'abc123', '111:1000', { kind: 'unverified', unverified: true })
      const before = rec(tab.id)!

      vi.setSystemTime(9_000)
      backfill({ tmuxInstance: '111:1000', agent: answer, cwd: '/w/answer' })

      expect(rec(tab.id)).toEqual({ ...before, unverified: undefined })
      // Spelled out too, because the equality above would also pass if `before`
      // were somehow the object being compared to itself.
      expect(rec(tab.id)?.agent).toEqual({ type: 'cc', sessionId: 'S1', tmuxPaneId: '%9', updatedAt: 1 })
      expect(rec(tab.id)?.cwd).toBe('/w/old')
      expect(rec(tab.id)?.cwdSource).toBe('agent-session-start')
      // Confirm lands the SAME identity, so the user's edit is not stale.
      expect(rec(tab.id)?.resumeCommandOverride).toBe('cld-yolo -c')
      expect(rec(tab.id)?.capturedAt).toBe(before.capturedAt)
    } finally {
      vi.useRealTimers()
    }
  })

  it('mode 3 (confirm): the batch still resolves to the record the user edited', () => {
    // `capturedAt` is not a timestamp for display: `groupForBatch` reads it to
    // pick WHICH pane's record a group rebuilds from. Re-stamping it on a
    // confirm hands that election to whichever sibling happened to be alive
    // when the daemon answered — and edits are pane-scoped, so that sibling is
    // exactly the one that never saw what the user typed.
    vi.useFakeTimers()
    try {
      vi.setSystemTime(1_000)
      const tab = seed()
      useTabStore.setState({
        tabs: { [tab.id]: splitTabWithSecondPane(tab, 'p2') },
        tabOrder: [tab.id],
        activeTabId: tab.id,
      })
      seedAgentGroup({
        tmuxInstance: '111:1000', cwd: '/w/old', cwdSource: 'agent-session-start',
        agent: { type: 'cc', sessionId: 'S1', updatedAt: 1 },
        capturedAt: 1,
      })
      useTabStore.getState().setPaneRebuild('h1', 'abc123', '111:1000', {
        kind: 'unverified', unverified: true,
      })

      // p1's session pane dies and the user types its resume command (§4.10).
      vi.setSystemTime(3_000)
      killPane(tab.id, 'p1')
      useTabStore.getState().setPaneRebuildForPane(
        tab.id, 'p1', { hostId: 'h1', sessionCode: 'abc123', tmuxInstance: '111:1000' },
        { kind: 'field', field: 'resumeCommandOverride', value: 'cld-yolo -c' },
      )

      // The daemon's agreeing answer reaches the still-live sibling only.
      vi.setSystemTime(4_000)
      backfill({ tmuxInstance: '111:1000', agent: answer, cwd: '/w/answer' })
      vi.setSystemTime(5_000)
      killPane(tab.id, 'p2')

      const { groups } = groupForBatch(batchCandidates(collectRecordRows(useTabStore.getState().tabs)))
      expect(groups).toHaveLength(1)
      expect(groups[0].sourcePaneId).toBe('p1')
      expect(groups[0].record.resumeCommandOverride).toBe('cld-yolo -c')
    } finally {
      vi.useRealTimers()
    }
  })

  it('mode 4 (no-op): an agent present and verified is left alone, same identity', () => {
    // The case v3's unordered table let match two rows at once.
    const tab = seed()
    const paneId = getPrimaryPane(tab.layout).id
    seedAgentGroup({
      tmuxInstance: '111:1000', cwd: '/w/old', cwdSource: 'agent-session-start',
      agent: { type: 'cc', sessionId: 'S1', updatedAt: 1 },
      capturedAt: 1,
    })
    const before = paneContentOf(tab.id, paneId)
    backfill({ tmuxInstance: '111:1000', agent: answer, cwd: '/w/answer' })
    expect(paneContentOf(tab.id, paneId)).toBe(before)
  })

  it('mode 4 (no-op): a verified record is left alone even when the answer disagrees', () => {
    // "有了就跳過": only `unverified` licenses a correction.
    const tab = seed()
    const paneId = getPrimaryPane(tab.layout).id
    seedAgentGroup({
      tmuxInstance: '111:1000', cwd: '/w/old', cwdSource: 'agent-session-start',
      agent: { type: 'codex', sessionId: 'OLD', updatedAt: 1 },
      capturedAt: 1,
    })
    const before = paneContentOf(tab.id, paneId)
    backfill({ tmuxInstance: '111:1000', agent: answer, cwd: '/w/answer' })
    expect(paneContentOf(tab.id, paneId)).toBe(before)
  })

  it('the generation guard rejects a mismatched instance', () => {
    const tab = seed('111:1000')
    useTabStore.getState().setPaneRebuild('h1', 'abc123', '222:2000', {
      kind: 'agent-backfill',
      record: { tmuxInstance: '222:2000', agent: answer, cwd: '/w/answer' },
    })
    expect(rec(tab.id)).toBeUndefined()
  })

  it('a later agent-group still overwrites everything the backfill wrote', () => {
    const tab = seed()
    backfill({ tmuxInstance: '111:1000', agent: answer, cwd: '/w/answer' })
    seedAgentGroup({
      tmuxInstance: '111:1000', cwd: '/w/fresh', cwdSource: 'agent-session-start',
      agent: { type: 'opencode', sessionId: 'ses_x', updatedAt: 9 },
      capturedAt: 9,
    })
    expect(rec(tab.id)?.agent?.type).toBe('opencode')
    expect(rec(tab.id)?.cwd).toBe('/w/fresh')
    expect(rec(tab.id)?.cwdSource).toBe('agent-session-start')
    expect(resolveResumeCommand(rec(tab.id), defaultTemplates)).toBe('opencode -s ses_x')
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
