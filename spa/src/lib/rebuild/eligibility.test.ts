// spa/src/lib/rebuild/eligibility.test.ts — the one verdict the records table
// and "Rebuild all" both read (spec §4.11).
//
// The invariant every case here pins: `rebuildable` is exactly `health ===
// 'dead'`, so the badge a row shows and the action the button takes can never
// tell different stories.
import { describe, it, expect, beforeEach } from 'vitest'
import {
  batchCandidates,
  classifyRecord,
  collectRecordRows,
  recordHealth,
  type RecordRow,
} from './eligibility'
import { useTabStore } from '../../stores/useTabStore'
import type { PaneRebuildRecord, Tab, TmuxSessionContent } from '../../types/tab'

const record = (over: Partial<PaneRebuildRecord> = {}): PaneRebuildRecord => ({
  sessionName: 'dev', tmuxInstance: '111:1000', cwd: '/w', capturedAt: 1, ...over,
})

const row = (over: Partial<RecordRow> = {}): RecordRow => ({
  paneId: 'p1', tabId: 't1', hostId: 'h1', sessionCode: 'old111',
  tmuxInstance: '111:1000', cachedName: 'dev',
  terminated: 'tmux-restarted', record: record(),
  ...over,
})

describe('classifyRecord', () => {
  it('a dead pane with a cwd is the one thing a rebuild may act on', () => {
    expect(classifyRecord(row())).toEqual({ health: 'dead', rebuildable: true })
  })

  it('an attached pane is live and never rebuilt, whatever the live list says', () => {
    // The generation guard, not a code+name comparison, is what decides this:
    // after a tmux restart the same code can be alive again under a new
    // generation and even carry the same name, and the pane bound to the old
    // one is still dead.
    expect(classifyRecord(row({ terminated: undefined })))
      .toEqual({ health: 'live', rebuildable: false, reason: 'attached' })
    expect(classifyRecord(row({ terminated: 'tmux-restarted' })).rebuildable).toBe(true)
  })

  it('a record with no cwd is structure-only and NOT auto-rebuildable', () => {
    // Stated once, here: the engine would create the session happily and the
    // agent would come back in the daemon's default directory instead of the
    // one the work lives in. ⚠️ and "the batch skips it" are the same fact.
    const verdict = classifyRecord(row({ record: record({ cwd: undefined }) }))
    expect(verdict).toEqual({ health: 'structure', rebuildable: false, reason: 'no-cwd' })
  })

  it('a dead pane that never captured a record is structure-only', () => {
    expect(classifyRecord(row({ record: undefined })))
      .toEqual({ health: 'structure', rebuildable: false, reason: 'no-record' })
  })

  it('a pane whose host was removed reads offline and is never rebuilt', () => {
    expect(classifyRecord(row({ terminated: 'host-removed' })))
      .toEqual({ health: 'offline', rebuildable: false, reason: 'host-removed' })
  })

  it('rebuildable is exactly the dead rows', () => {
    const rows = [
      row({ paneId: 'dead' }),
      row({ paneId: 'live', terminated: undefined }),
      row({ paneId: 'nocwd', record: record({ cwd: undefined }) }),
      row({ paneId: 'norecord', record: undefined }),
      row({ paneId: 'gone', terminated: 'host-removed' }),
    ]
    for (const r of rows) {
      const { health, rebuildable } = classifyRecord(r)
      expect(rebuildable).toBe(health === 'dead')
    }
  })
})

describe('recordHealth', () => {
  it('masks a row while its host list is still loading', () => {
    expect(recordHealth(row(), 'loading')).toBe('loading')
  })

  it('greys every row out on an unreachable host', () => {
    expect(recordHealth(row(), 'offline')).toBe('offline')
    expect(recordHealth(row({ terminated: undefined }), 'offline')).toBe('offline')
  })

  it('a removed host outranks whatever the fetch says', () => {
    expect(recordHealth(row({ terminated: 'host-removed' }), 'online')).toBe('offline')
    expect(recordHealth(row({ terminated: 'host-removed' }), 'loading')).toBe('offline')
  })

  it('passes the row verdict through once the host is known reachable', () => {
    expect(recordHealth(row(), 'online')).toBe('dead')
    expect(recordHealth(row({ terminated: undefined }), 'online')).toBe('live')
    expect(recordHealth(row({ record: record({ cwd: undefined }) }), 'online')).toBe('structure')
  })
})

// ---------------------------------------------------------------------------
// collectRecordRows / batchCandidates
// ---------------------------------------------------------------------------

function content(hostId: string, over: Partial<TmuxSessionContent> = {}): TmuxSessionContent {
  return {
    kind: 'tmux-session', hostId, sessionCode: 'old111', mode: 'terminal',
    cachedName: 'dev', tmuxInstance: '111:1000', terminated: 'tmux-restarted',
    rebuild: record({ agent: { type: 'cc', sessionId: 'S1', updatedAt: 1 } }),
    ...over,
  }
}

/** Add one single-pane tab, keeping the tabs already seeded. */
function seedPane(tabId: string, paneId: string, over: Partial<TmuxSessionContent> = {}) {
  const tab: Tab = {
    id: tabId, pinned: false, locked: false, createdAt: 0,
    layout: { type: 'leaf', pane: { id: paneId, content: content('h1', over) } },
  }
  const prev = useTabStore.getState()
  useTabStore.setState({
    tabs: { ...prev.tabs, [tabId]: tab },
    tabOrder: [...prev.tabOrder.filter((id) => id !== tabId), tabId],
    activeTabId: tabId,
  })
}

describe('collectRecordRows', () => {
  beforeEach(() => useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null }))

  it('collects every terminal tmux pane across tabs, live ones included', () => {
    seedPane('t1', 'p1')
    seedPane('t2', 'p2', { terminated: undefined })
    const rows = collectRecordRows(useTabStore.getState().tabs)
    expect(rows.map((r) => r.paneId)).toEqual(['p1', 'p2'])
    expect(rows[1].terminated).toBeUndefined()
  })

  it('ignores stream panes and non-tmux panes', () => {
    seedPane('t1', 'p1', { mode: 'stream' })
    useTabStore.setState({
      tabs: {
        ...useTabStore.getState().tabs,
        t2: { id: 't2', pinned: false, locked: false, createdAt: 0,
              layout: { type: 'leaf', pane: { id: 'p2', content: { kind: 'new-tab' } } } },
      },
    })
    expect(collectRecordRows(useTabStore.getState().tabs)).toEqual([])
  })

  it('keeps only the rows classifyRecord calls rebuildable', () => {
    seedPane('t1', 'p1')
    seedPane('t2', 'p2', { terminated: undefined })                              // live
    seedPane('t3', 'p3', { rebuild: undefined })                                 // no record
    seedPane('t4', 'p4', { terminated: 'host-removed' })                         // no host to build on
    seedPane('t5', 'p5', { rebuild: record({ cwd: undefined }) })                // nowhere to launch
    expect(batchCandidates(collectRecordRows(useTabStore.getState().tabs)).map((c) => c.paneId)).toEqual(['p1'])
  })
})
