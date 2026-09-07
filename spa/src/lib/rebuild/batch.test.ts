// spa/src/lib/rebuild/batch.test.ts — grouping and batch orchestration (spec §4.11).
import { describe, it, expect, vi, beforeEach } from 'vitest'
import {
  BATCH_LOCK_OWNER,
  batchCandidates,
  collectRecordRows,
  groupForBatch,
  runBatchRebuild,
  type BatchCandidate,
} from './batch'
import { useRebuildStore } from '../../stores/useRebuildStore'
import { useHostStore } from '../../stores/useHostStore'
import { useTabStore } from '../../stores/useTabStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { rebuildPane } from './engine'
import type { Session } from '../host-api'
import type { PaneRebuildRecord, Tab, TmuxSessionContent } from '../../types/tab'

// ---------------------------------------------------------------------------
// groupForBatch — the pure part
// ---------------------------------------------------------------------------

const pane = (paneId: string, over: Partial<BatchCandidate> = {}): BatchCandidate => ({
  paneId, tabId: 't1', hostId: 'h1', sessionCode: 'abc', tmuxInstance: '111:1000',
  record: {
    sessionName: 'dev', tmuxInstance: '111:1000', cwd: '/w', capturedAt: 1,
    agent: { type: 'cc', sessionId: 'S1', updatedAt: 1 }, resumeCommand: 'claude --resume S1',
  },
  ...over,
})

describe('groupForBatch', () => {
  it('merges two panes on the same dead session into one group', () => {
    const { groups } = groupForBatch([pane('p1'), pane('p2')])
    expect(groups).toHaveLength(1)
    expect(groups[0].paneIds).toEqual(['p1', 'p2'])
  })

  it('keeps the same code under different generations apart', () => {
    const { groups } = groupForBatch([pane('p1'), pane('p2', { tmuxInstance: '222:2000' })])
    expect(groups).toHaveLength(2)
  })

  it('keeps the same code on different hosts apart', () => {
    const { groups } = groupForBatch([pane('p1'), pane('p2', { hostId: 'h2' })])
    expect(groups).toHaveLength(2)
  })

  it('excludes panes with an unknown generation', () => {
    const { groups, excluded } = groupForBatch([pane('p1'), pane('p2', { tmuxInstance: '' })])
    expect(groups).toHaveLength(1)
    expect(excluded.map((p) => p.paneId)).toEqual(['p2'])
  })

  it('resolves conflicting hand-edits to the latest capturedAt', () => {
    const older = pane('p1')
    const newer = pane('p2', { record: { ...pane('p2').record, cwd: '/w/newer', capturedAt: 9 } })
    const { groups } = groupForBatch([older, newer])
    expect(groups[0].record.cwd).toBe('/w/newer')
    expect(groups[0].sourcePaneId).toBe('p2')
    // The loser is still re-pointed onto the winner's result.
    expect(groups[0].paneIds).toEqual(['p1', 'p2'])
  })

  it('skips the exact resume for unverified records', () => {
    const { groups } = groupForBatch([pane('p1', { record: { ...pane('p1').record, unverified: true } })])
    expect(groups[0].plan.runResume).toBe(false)
  })

  it('skips the cwd for a record that never captured one', () => {
    const { groups } = groupForBatch([pane('p1', { record: { ...pane('p1').record, cwd: undefined } })])
    expect(groups[0].plan.applyCwd).toBe(false)
    expect(groups[0].plan.createSession).toBe(true)
  })
})

// ---------------------------------------------------------------------------
// Fixtures for everything that reads the stores
// ---------------------------------------------------------------------------

function session(over: Partial<Session>): Session {
  return { code: 'c', name: 'n', cwd: '', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false, ...over }
}

function seedHost(hostId: string) {
  useHostStore.setState({
    hosts: { [hostId]: { id: hostId, name: hostId, ip: '127.0.0.1', port: 7860, token: null, order: 0 } },
    hostOrder: [hostId], activeHostId: hostId, runtime: { [hostId]: { status: 'connected', attachReady: true } },
  })
}

function content(hostId: string, over: Partial<TmuxSessionContent> = {}): TmuxSessionContent {
  return {
    kind: 'tmux-session', hostId, sessionCode: 'old111', mode: 'terminal',
    cachedName: 'dev', tmuxInstance: '111:1000', terminated: 'tmux-restarted',
    rebuild: { sessionName: 'dev', tmuxInstance: '111:1000', cwd: '/w', capturedAt: 1,
      resumeCommand: 'claude --resume S1' },
    ...over,
  }
}

/** Add one single-pane tab, keeping the tabs already seeded. */
function seedPane(hostId: string, tabId: string, paneId: string, record: Partial<PaneRebuildRecord> = {},
                  over: Partial<TmuxSessionContent> = {}) {
  const base = content(hostId, over)
  const tab: Tab = {
    id: tabId, pinned: false, locked: false, createdAt: 0,
    layout: { type: 'leaf', pane: { id: paneId, content: {
      ...base,
      rebuild: base.rebuild ? { ...base.rebuild, ...record } : undefined,
    } } },
  }
  const prev = useTabStore.getState()
  useTabStore.setState({
    tabs: { ...prev.tabs, [tabId]: tab },
    tabOrder: [...prev.tabOrder.filter((id) => id !== tabId), tabId],
    activeTabId: tabId,
  })
}

function paneContent(tabId: string, paneId: string): TmuxSessionContent {
  const layout = useTabStore.getState().tabs[tabId].layout
  if (layout.type !== 'leaf' || layout.pane.id !== paneId) throw new Error('fixture is a leaf')
  const c = layout.pane.content
  if (c.kind !== 'tmux-session') throw new Error('fixture is a tmux pane')
  return c
}

function sessionCodeOfPane(tabId: string, paneId: string): string {
  return paneContent(tabId, paneId).sessionCode
}

/** Re-point a pane at someone else's session, mid-operation. */
function rebindPane(tabId: string, _paneId: string, sessionCode: string) {
  const tab = useTabStore.getState().tabs[tabId]
  const layout = tab.layout
  if (layout.type !== 'leaf') throw new Error('fixture is a leaf')
  useTabStore.setState({
    tabs: {
      ...useTabStore.getState().tabs,
      [tabId]: { ...tab, layout: { ...layout, pane: { ...layout.pane, content: { ...layout.pane.content, sessionCode } as never } } },
    },
  })
}

function resetStores() {
  useRebuildStore.setState({ operations: {}, lockedBy: null })
  useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
}

// ---------------------------------------------------------------------------
// collectRecordRows / batchCandidates
// ---------------------------------------------------------------------------

describe('collectRecordRows', () => {
  beforeEach(resetStores)

  it('collects every terminal tmux pane across tabs, live ones included', () => {
    seedPane('h1', 't1', 'p1')
    seedPane('h1', 't2', 'p2', {}, { terminated: undefined })
    const rows = collectRecordRows(useTabStore.getState().tabs)
    expect(rows.map((r) => r.paneId)).toEqual(['p1', 'p2'])
    expect(rows[1].terminated).toBeUndefined()
  })

  it('ignores stream panes and non-tmux panes', () => {
    seedPane('h1', 't1', 'p1', {}, { mode: 'stream' })
    useTabStore.setState({
      tabs: {
        ...useTabStore.getState().tabs,
        t2: { id: 't2', pinned: false, locked: false, createdAt: 0,
              layout: { type: 'leaf', pane: { id: 'p2', content: { kind: 'new-tab' } } } },
      },
    })
    expect(collectRecordRows(useTabStore.getState().tabs)).toEqual([])
  })

  it('keeps only dead panes that still carry a record as batch candidates', () => {
    seedPane('h1', 't1', 'p1')
    seedPane('h1', 't2', 'p2', {}, { terminated: undefined })          // live
    seedPane('h1', 't3', 'p3', {}, { rebuild: undefined })             // no record
    seedPane('h1', 't4', 'p4', {}, { terminated: 'host-removed' })     // no host to build on
    expect(batchCandidates(collectRecordRows(useTabStore.getState().tabs)).map((c) => c.paneId)).toEqual(['p1'])
  })
})

// ---------------------------------------------------------------------------
// runBatchRebuild — the orchestration the grouping exists for
// ---------------------------------------------------------------------------

describe('runBatchRebuild', () => {
  beforeEach(() => {
    resetStores()
    vi.unstubAllGlobals()
    seedHost('h1')
  })

  it('creates one session for a group and re-points every member, across tabs', async () => {
    seedPane('h1', 't1', 'p1', { sessionName: 'dev' })
    seedPane('h1', 't2', 'p2', { sessionName: 'dev' })   // same dead session, other tab
    const create = vi.fn(async () => session({ code: 'new1', name: 'dev', tmux_instance: '222:2000' }))
    const sendKeys = vi.fn()

    const report = await runBatchRebuild({ createSession: create, sendKeys })

    expect(report.status).toBe('ok')
    expect(create).toHaveBeenCalledTimes(1)
    expect(sendKeys).toHaveBeenCalledTimes(1)
    expect(sessionCodeOfPane('t1', 'p1')).toBe('new1')
    expect(sessionCodeOfPane('t2', 'p2')).toBe('new1')
    expect(report.groups[0].members.map((m) => [m.paneId, m.repointed])).toEqual([['p2', true]])
  })

  it('re-verifies each member binding before re-pointing it', async () => {
    seedPane('h1', 't1', 'p1', { sessionName: 'dev' })
    seedPane('h1', 't2', 'p2', { sessionName: 'dev' })
    await runBatchRebuild({
      createSession: vi.fn(async () => {
        rebindPane('t2', 'p2', 'someone-else')
        return session({ code: 'new1', name: 'dev', tmux_instance: '222:2000' })
      }),
      sendKeys: vi.fn(),
    })
    expect(sessionCodeOfPane('t1', 'p1')).toBe('new1')
    expect(sessionCodeOfPane('t2', 'p2')).toBe('someone-else')
  })

  it('runs one create per group and leaves the unknown generation out', async () => {
    seedPane('h1', 't1', 'p1', { sessionName: 'dev' })
    seedPane('h1', 't2', 'p2', { sessionName: 'other' }, { sessionCode: 'old222' })
    seedPane('h1', 't3', 'p3', { sessionName: 'lost' }, { tmuxInstance: '' })
    const create = vi.fn(async () => session({ code: 'new1', name: 'dev', tmux_instance: '222:2000' }))

    const report = await runBatchRebuild({ createSession: create, sendKeys: vi.fn() })

    expect(create).toHaveBeenCalledTimes(2)
    expect(report.excluded.map((p) => p.paneId)).toEqual(['p3'])
    expect(sessionCodeOfPane('t3', 'p3')).toBe('old111')
  })

  it('skips the resume for an unverified record', async () => {
    seedPane('h1', 't1', 'p1', { sessionName: 'dev', unverified: true })
    const sendKeys = vi.fn()
    const report = await runBatchRebuild({
      createSession: vi.fn(async () => session({ code: 'new1', name: 'dev', tmux_instance: '222:2000' })),
      sendKeys,
    })
    expect(sendKeys).not.toHaveBeenCalled()
    expect(report.groups[0].report.steps.resume.status).toBe('skipped')
    expect(sessionCodeOfPane('t1', 'p1')).toBe('new1')
  })

  it('takes the lock once as rebuild:batch and lets no engine call re-acquire it', async () => {
    seedPane('h1', 't1', 'p1', { sessionName: 'dev' })
    seedPane('h1', 't2', 'p2', { sessionName: 'dev' })
    const held: (string | null)[] = []
    await runBatchRebuild({
      createSession: vi.fn(async () => {
        held.push(useRebuildStore.getState().lockedBy)
        return session({ code: 'new1', name: 'dev', tmux_instance: '222:2000' })
      }),
      sendKeys: vi.fn(),
    })
    expect(held).toEqual([BATCH_LOCK_OWNER])
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })

  it('refuses to start while a single-pane rebuild holds the lock', async () => {
    seedPane('h1', 't1', 'p1', { sessionName: 'dev' })
    useRebuildStore.getState().acquireOperationLock('rebuild:p1')
    const create = vi.fn()
    const report = await runBatchRebuild({ createSession: create, sendKeys: vi.fn() })
    expect(report.status).toBe('blocked')
    expect(report.blockedBy).toBe('rebuild:p1')
    expect(create).not.toHaveBeenCalled()
    expect(useRebuildStore.getState().lockedBy).toBe('rebuild:p1')
  })

  it('blocks a single-pane rebuild while the batch is running', async () => {
    seedPane('h1', 't1', 'p1', { sessionName: 'dev' })
    // A pane the batch is NOT acting on (still live), so the refusal can only
    // come from the lock — not from the engine's same-pane guard.
    seedPane('h1', 't2', 'p2', { sessionName: 'other' }, { terminated: undefined, sessionCode: 'old222' })
    let blocked: Awaited<ReturnType<typeof rebuildPane>> | undefined
    await runBatchRebuild({
      createSession: vi.fn(async () => {
        blocked = await rebuildPane('h1', 't2', 'p2', { createSession: true, applyCwd: true, runResume: false })
        return session({ code: 'new1', name: 'dev', tmux_instance: '222:2000' })
      }),
      sendKeys: vi.fn(),
    })
    expect(blocked?.steps.create.status).toBe('failed')
    expect(blocked?.steps.create.error).toContain(BATCH_LOCK_OWNER)
  })

  it('reports an empty run without taking the lock hostage', async () => {
    const report = await runBatchRebuild({ createSession: vi.fn(), sendKeys: vi.fn() })
    expect(report.status).toBe('ok')
    expect(report.groups).toEqual([])
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })
})

describe('rebuildPane — a caller-supplied lock token', () => {
  beforeEach(() => {
    resetStores()
    seedHost('h1')
    seedPane('h1', 't1', 'p1', { sessionName: 'dev' })
  })

  it('refuses a token that is no longer the current holder', async () => {
    const token = useRebuildStore.getState().acquireOperationLock(BATCH_LOCK_OWNER)!
    useRebuildStore.getState().releaseOperationLock(token)
    useRebuildStore.getState().acquireOperationLock('snapshot:restoreAll')
    const create = vi.fn()
    const report = await rebuildPane('h1', 't1', 'p1', { createSession: true, applyCwd: true, runResume: false },
      { createSession: create, sendKeys: vi.fn(), lockToken: token })
    expect(create).not.toHaveBeenCalled()
    expect(report.steps.create.status).toBe('failed')
    expect(useRebuildStore.getState().lockedBy).toBe('snapshot:restoreAll')
  })

  it('does not release the caller lock when the borrowed run ends', async () => {
    const token = useRebuildStore.getState().acquireOperationLock(BATCH_LOCK_OWNER)!
    await rebuildPane('h1', 't1', 'p1', { createSession: true, applyCwd: true, runResume: false }, {
      createSession: vi.fn(async () => session({ code: 'new1', name: 'dev', tmux_instance: '222:2000' })),
      sendKeys: vi.fn(),
      lockToken: token,
    })
    expect(useRebuildStore.getState().lockedBy).toBe(BATCH_LOCK_OWNER)
    useRebuildStore.getState().releaseOperationLock(token)
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })
})
