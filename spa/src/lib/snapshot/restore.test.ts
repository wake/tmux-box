import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createSession, listSessions } from '../host-api'
import type { Session } from '../host-api'
import type { PaneContent, PaneLayout, Tab, Workspace } from '../../types/tab'
import type { Remap } from './types'
import type { SessionMeta, WorkspaceSnapshot } from './types'
import { RestoreError } from './types'
import {
  ensureSessions,
  rebuildAllSessions,
  remapLayoutSessions,
  replaceTabSnapshot,
  replaceTabState,
  restoreAll,
  restoreTabLayout,
  syncSessionStore,
  undoLastRestore,
  validateSnapshotConsistency,
} from './restore'
import { readPrevSnapshot } from './storage'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useSessionStore } from '../../stores/useSessionStore'
import { useRebuildStore } from '../../stores/useRebuildStore'

vi.mock('../host-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../host-api')>()),
  listSessions: vi.fn(),
  createSession: vi.fn(),
}))

function session(overrides: Partial<Session> & { code: string }): Session {
  return {
    name: overrides.name ?? overrides.code,
    cwd: overrides.cwd ?? '/tmp',
    mode: overrides.mode ?? 'terminal',
    cc_session_id: '',
    cc_model: '',
    has_relay: false,
    ...overrides,
  }
}

function meta(overrides: Partial<SessionMeta> & { hostId: string; sessionCode: string }): SessionMeta {
  return {
    name: overrides.name ?? overrides.sessionCode,
    mode: overrides.mode ?? 'terminal',
    cwd: overrides.cwd ?? '/work',
    restorable: overrides.restorable ?? true,
    ...overrides,
  }
}

describe('ensureSessions', () => {
  beforeEach(() => {
    vi.mocked(listSessions).mockReset()
    vi.mocked(createSession).mockReset()
  })

  it('1. all sessions live → all reattached; createSession never called', async () => {
    const sessionMeta: Record<string, Record<string, SessionMeta>> = {
      hostA: {
        // Reattach now requires code AND name match, so the meta name must equal
        // the live session's name (same session survived the daemon restart).
        code1: meta({ hostId: 'hostA', sessionCode: 'code1', name: 'live1' }),
        code2: meta({ hostId: 'hostA', sessionCode: 'code2', name: 'live2' }),
      },
    }
    vi.mocked(listSessions).mockResolvedValue([
      session({ code: 'code1', name: 'live1' }),
      session({ code: 'code2', name: 'live2' }),
    ])

    const { remap, report } = await ensureSessions(sessionMeta)

    expect(listSessions).toHaveBeenCalledTimes(1)
    expect(createSession).not.toHaveBeenCalled()
    expect(remap.hostA.code1.status).toBe('reattached')
    expect(remap.hostA.code2.status).toBe('reattached')
    if (remap.hostA.code1.status === 'reattached') {
      expect(remap.hostA.code1.newCode).toBe('code1')
      expect(remap.hostA.code1.session.name).toBe('live1')
    }
    expect(report).toEqual({ reattached: 2, rebuilt: 0, failed: 0 })
  })

  it('2. rebuild scope: 3 restorable dead (incl orphans) + 1 live → createSession exactly 3', async () => {
    const sessionMeta: Record<string, Record<string, SessionMeta>> = {
      hostA: {
        live1: meta({ hostId: 'hostA', sessionCode: 'live1' }),
        dead1: meta({ hostId: 'hostA', sessionCode: 'dead1', name: 'd1', cwd: '/a' }),
        dead2: meta({ hostId: 'hostA', sessionCode: 'dead2', name: 'd2', cwd: '/b' }),
        orphan: meta({ hostId: 'hostA', sessionCode: 'orphan', name: 'd3', cwd: '/c' }),
      },
    }
    vi.mocked(listSessions).mockResolvedValue([session({ code: 'live1', name: 'live1' })])
    vi.mocked(createSession).mockImplementation(async (_h, name) =>
      session({ code: `new-${name}`, name }),
    )

    const { remap, report } = await ensureSessions(sessionMeta)

    expect(createSession).toHaveBeenCalledTimes(3)
    expect(remap.hostA.live1.status).toBe('reattached')
    expect(remap.hostA.dead1.status).toBe('rebuilt')
    expect(remap.hostA.dead2.status).toBe('rebuilt')
    expect(remap.hostA.orphan.status).toBe('rebuilt')
    expect(report).toEqual({ reattached: 1, rebuilt: 3, failed: 0 })
  })

  it('3. dead entry with restorable=false → failed; createSession not called for it', async () => {
    const sessionMeta: Record<string, Record<string, SessionMeta>> = {
      hostA: {
        dead: meta({ hostId: 'hostA', sessionCode: 'dead', restorable: false, cwd: undefined }),
      },
    }
    vi.mocked(listSessions).mockResolvedValue([])

    const { remap, report } = await ensureSessions(sessionMeta)

    expect(createSession).not.toHaveBeenCalled()
    expect(remap.hostA.dead.status).toBe('failed')
    expect(report).toEqual({ reattached: 0, rebuilt: 0, failed: 1 })
  })

  it('4. rebuild:false → dead entries failed, createSession never called; live still reattached', async () => {
    const sessionMeta: Record<string, Record<string, SessionMeta>> = {
      hostA: {
        live1: meta({ hostId: 'hostA', sessionCode: 'live1' }),
        dead1: meta({ hostId: 'hostA', sessionCode: 'dead1' }),
      },
    }
    vi.mocked(listSessions).mockResolvedValue([session({ code: 'live1', name: 'live1' })])

    const { remap, report } = await ensureSessions(sessionMeta, { rebuild: false })

    expect(createSession).not.toHaveBeenCalled()
    expect(remap.hostA.live1.status).toBe('reattached')
    expect(remap.hostA.dead1.status).toBe('failed')
    expect(report).toEqual({ reattached: 1, rebuilt: 0, failed: 1 })
  })

  it('5. createSession rejects for one entry → that entry failed, the rest proceed', async () => {
    const sessionMeta: Record<string, Record<string, SessionMeta>> = {
      hostA: {
        dead1: meta({ hostId: 'hostA', sessionCode: 'dead1', name: 'd1' }),
        dead2: meta({ hostId: 'hostA', sessionCode: 'dead2', name: 'd2' }),
      },
    }
    vi.mocked(listSessions).mockResolvedValue([])
    vi.mocked(createSession).mockImplementation(async (_h, name) => {
      if (name === 'd1') throw new Error('daemon boom')
      return session({ code: `new-${name}`, name })
    })

    const { remap, report } = await ensureSessions(sessionMeta)

    expect(createSession).toHaveBeenCalledTimes(2)
    expect(remap.hostA.dead1.status).toBe('failed')
    expect(remap.hostA.dead2.status).toBe('rebuilt')
    expect(report).toEqual({ reattached: 0, rebuilt: 1, failed: 1 })
  })

  it('6. host offline (listSessions throws) → every entry failed; createSession never called', async () => {
    const sessionMeta: Record<string, Record<string, SessionMeta>> = {
      hostA: {
        a: meta({ hostId: 'hostA', sessionCode: 'a' }),
        b: meta({ hostId: 'hostA', sessionCode: 'b' }),
      },
    }
    vi.mocked(listSessions).mockRejectedValue(new Error('host unreachable'))

    const { remap, report } = await ensureSessions(sessionMeta)

    expect(createSession).not.toHaveBeenCalled()
    expect(remap.hostA.a.status).toBe('failed')
    expect(remap.hostA.b.status).toBe('failed')
    expect(report).toEqual({ reattached: 0, rebuilt: 0, failed: 2 })
  })

  it('7. cross-host same code: A live (reattached), B dead (rebuilt), no contamination', async () => {
    const sessionMeta: Record<string, Record<string, SessionMeta>> = {
      A: { shared: meta({ hostId: 'A', sessionCode: 'shared', name: 'a-name' }) },
      B: { shared: meta({ hostId: 'B', sessionCode: 'shared', name: 'b-name', cwd: '/b' }) },
    }
    vi.mocked(listSessions).mockImplementation(async (hostId: string) => {
      // Reattach requires code AND name match: host A's live session shares the
      // meta's name ('a-name'), so it legitimately reattaches.
      if (hostId === 'A') return [session({ code: 'shared', name: 'a-name' })]
      if (hostId === 'B') return []
      throw new Error(`unexpected host ${hostId}`)
    })
    vi.mocked(createSession).mockImplementation(async (_h, name) =>
      session({ code: 'b-new-code', name }),
    )

    const { remap, report } = await ensureSessions(sessionMeta)

    expect(remap.A.shared.status).toBe('reattached')
    expect(remap.B.shared.status).toBe('rebuilt')
    if (remap.B.shared.status === 'rebuilt') {
      expect(remap.B.shared.newCode).toBe('b-new-code')
    }
    expect(remap.A.shared).not.toEqual(remap.B.shared)
    expect(report).toEqual({ reattached: 1, rebuilt: 1, failed: 0 })
  })

  it('8. daemon auto-rename (§8.1): trust returned object for newCode + name', async () => {
    const sessionMeta: Record<string, Record<string, SessionMeta>> = {
      hostA: {
        dead1: meta({ hostId: 'hostA', sessionCode: 'dead1', name: 'requested-name', cwd: '/w' }),
      },
    }
    vi.mocked(listSessions).mockResolvedValue([])
    vi.mocked(createSession).mockResolvedValue(
      session({ code: 'daemon-assigned-code', name: 'daemon-renamed' }),
    )

    const { remap } = await ensureSessions(sessionMeta)

    const entry = remap.hostA.dead1
    expect(entry.status).toBe('rebuilt')
    if (entry.status === 'rebuilt') {
      expect(entry.newCode).toBe('daemon-assigned-code')
      expect(entry.session.name).toBe('daemon-renamed')
      expect(entry.session.code).toBe('daemon-assigned-code')
    }
  })

  it('9. code AND name match → reattached; createSession NOT called', async () => {
    const sessionMeta: Record<string, Record<string, SessionMeta>> = {
      hostA: {
        code1: meta({ hostId: 'hostA', sessionCode: 'code1', name: 'same-name', cwd: '/w' }),
      },
    }
    vi.mocked(listSessions).mockResolvedValue([session({ code: 'code1', name: 'same-name' })])

    const { remap, report } = await ensureSessions(sessionMeta)

    expect(createSession).not.toHaveBeenCalled()
    const entry = remap.hostA.code1
    expect(entry.status).toBe('reattached')
    if (entry.status === 'reattached') {
      expect(entry.newCode).toBe('code1')
      expect(entry.session.name).toBe('same-name')
    }
    expect(report).toEqual({ reattached: 1, rebuilt: 0, failed: 0 })
  })

  it('10. code matches but name differs (code reused for another session), restorable + rebuild default → rebuilt via meta; unrelated live session untouched', async () => {
    const sessionMeta: Record<string, Record<string, SessionMeta>> = {
      hostA: {
        code1: meta({ hostId: 'hostA', sessionCode: 'code1', name: 'my-name', cwd: '/mine', mode: 'terminal' }),
      },
    }
    // Live list has a DIFFERENT session that reused code1 after the restart.
    vi.mocked(listSessions).mockResolvedValue([session({ code: 'code1', name: 'someone-elses' })])
    vi.mocked(createSession).mockImplementation(async (_h, name, cwd, mode) =>
      session({ code: 'rebuilt-code', name, cwd, mode }),
    )

    const { remap, report } = await ensureSessions(sessionMeta)

    // Rebuild from the snapshot's meta, NOT adoption of the unrelated live session.
    expect(createSession).toHaveBeenCalledTimes(1)
    expect(createSession).toHaveBeenCalledWith('hostA', 'my-name', '/mine', 'terminal')
    const entry = remap.hostA.code1
    expect(entry.status).toBe('rebuilt')
    if (entry.status === 'rebuilt') {
      expect(entry.newCode).toBe('rebuilt-code')
      expect(entry.session.code).toBe('rebuilt-code')
    }
    expect(report).toEqual({ reattached: 0, rebuilt: 1, failed: 0 })
  })

  it('11. code matches but name differs, rebuild:false → failed; createSession NOT called', async () => {
    const sessionMeta: Record<string, Record<string, SessionMeta>> = {
      hostA: {
        code1: meta({ hostId: 'hostA', sessionCode: 'code1', name: 'my-name', cwd: '/mine' }),
      },
    }
    vi.mocked(listSessions).mockResolvedValue([session({ code: 'code1', name: 'someone-elses' })])

    const { remap, report } = await ensureSessions(sessionMeta, { rebuild: false })

    expect(createSession).not.toHaveBeenCalled()
    expect(remap.hostA.code1.status).toBe('failed')
    expect(report).toEqual({ reattached: 0, rebuilt: 0, failed: 1 })
  })
})

function tmuxPane(
  id: string,
  hostId: string,
  sessionCode: string,
  overrides?: Partial<Extract<PaneContent, { kind: 'tmux-session' }>>,
): { type: 'leaf'; pane: { id: string; content: PaneContent } } {
  return {
    type: 'leaf',
    pane: {
      id,
      content: {
        kind: 'tmux-session',
        hostId,
        sessionCode,
        mode: 'terminal',
        cachedName: overrides?.cachedName ?? sessionCode,
        tmuxInstance: overrides?.tmuxInstance ?? 'default',
        ...overrides,
      },
    },
  }
}

function tmux(content: PaneContent): Extract<PaneContent, { kind: 'tmux-session' }> {
  if (content.kind !== 'tmux-session') throw new Error('expected tmux-session pane')
  return content
}

describe('remapLayoutSessions', () => {
  it('1. rebuilt entry → sessionCode=newCode, cachedName updated, terminated cleared', () => {
    const layout = tmuxPane('p1', 'hostA', 'old1', {
      cachedName: 'stale',
      terminated: 'session-closed',
    })
    const remap: Remap = {
      hostA: {
        old1: {
          status: 'rebuilt',
          newCode: 'new1',
          session: session({ code: 'new1', name: 'fresh-name' }),
        },
      },
    }

    const result = remapLayoutSessions(layout, remap)
    const content = tmux((result as { pane: { content: PaneContent } }).pane.content)
    expect(content.sessionCode).toBe('new1')
    expect(content.cachedName).toBe('fresh-name')
    expect(content.terminated).toBeUndefined()
  })

  it('2. failed entry → terminated set to exactly "tmux-restarted"', () => {
    const layout = tmuxPane('p1', 'hostA', 'old1')
    const remap: Remap = { hostA: { old1: { status: 'failed' } } }

    const result = remapLayoutSessions(layout, remap)
    const content = tmux((result as { pane: { content: PaneContent } }).pane.content)
    expect(content.sessionCode).toBe('old1')
    expect(content.terminated).toBe('tmux-restarted')
  })

  it('3. onlyTerminated:true → live pane matching a remap oldCode is NOT modified', () => {
    const layout = tmuxPane('p1', 'hostA', 'old1', { cachedName: 'live-name' })
    const remap: Remap = {
      hostA: {
        old1: {
          status: 'rebuilt',
          newCode: 'new1',
          session: session({ code: 'new1', name: 'fresh-name' }),
        },
      },
    }

    const result = remapLayoutSessions(layout, remap, { onlyTerminated: true })
    const content = tmux((result as { pane: { content: PaneContent } }).pane.content)
    expect(content.sessionCode).toBe('old1')
    expect(content.cachedName).toBe('live-name')
    expect(content.terminated).toBeUndefined()
  })

  it('4. nested split with multiple tmux panes → all remapped by (hostId, sessionCode)', () => {
    const layout: PaneLayout = {
      type: 'split',
      id: 's1',
      direction: 'h',
      sizes: [50, 50],
      children: [
        tmuxPane('p1', 'hostA', 'a1', { terminated: 'session-closed' }),
        {
          type: 'split',
          id: 's2',
          direction: 'v',
          sizes: [50, 50],
          children: [
            tmuxPane('p2', 'hostB', 'b1'),
            tmuxPane('p3', 'hostA', 'a2', { terminated: 'host-removed' }),
          ],
        },
      ],
    }
    const remap: Remap = {
      hostA: {
        a1: { status: 'rebuilt', newCode: 'a1-new', session: session({ code: 'a1-new', name: 'A1' }) },
        a2: { status: 'failed' },
      },
      hostB: {
        b1: { status: 'reattached', newCode: 'b1', session: session({ code: 'b1', name: 'B1' }) },
      },
    }

    const result = remapLayoutSessions(layout, remap)
    const byId: Record<string, Extract<PaneContent, { kind: 'tmux-session' }>> = {}
    const walk = (l: PaneLayout): void => {
      if (l.type === 'leaf') {
        byId[l.pane.id] = tmux(l.pane.content)
      } else {
        l.children.forEach(walk)
      }
    }
    walk(result)

    expect(byId.p1.sessionCode).toBe('a1-new')
    expect(byId.p1.cachedName).toBe('A1')
    expect(byId.p1.terminated).toBeUndefined()

    expect(byId.p2.sessionCode).toBe('b1')
    expect(byId.p2.cachedName).toBe('B1')
    expect(byId.p2.terminated).toBeUndefined()

    expect(byId.p3.sessionCode).toBe('a2')
    expect(byId.p3.terminated).toBe('tmux-restarted')
  })

  it('5. no remap entry → pane left unchanged; non-tmux panes preserved', () => {
    const layout: PaneLayout = {
      type: 'split',
      id: 's1',
      direction: 'h',
      sizes: [50, 50],
      children: [
        tmuxPane('p1', 'hostA', 'unknown'),
        { type: 'leaf', pane: { id: 'p2', content: { kind: 'new-tab' } } },
      ],
    }
    const remap: Remap = {
      hostA: {
        other: { status: 'rebuilt', newCode: 'x', session: session({ code: 'x', name: 'X' }) },
      },
    }

    const result = remapLayoutSessions(layout, remap) as Extract<PaneLayout, { type: 'split' }>
    const first = tmux((result.children[0] as { pane: { content: PaneContent } }).pane.content)
    expect(first.sessionCode).toBe('unknown')
    const second = (result.children[1] as { pane: { content: PaneContent } }).pane.content
    expect(second.kind).toBe('new-tab')
  })

  it('6. pure: input layout is not mutated', () => {
    const layout = tmuxPane('p1', 'hostA', 'old1')
    const remap: Remap = {
      hostA: {
        old1: { status: 'rebuilt', newCode: 'new1', session: session({ code: 'new1', name: 'fresh' }) },
      },
    }

    const result = remapLayoutSessions(layout, remap)
    expect(tmux(layout.pane.content).sessionCode).toBe('old1')
    expect(result).not.toBe(layout)
    expect(tmux((result as { pane: { content: PaneContent } }).pane.content).sessionCode).toBe('new1')
  })
})

function tab(id: string): Tab {
  return {
    id,
    pinned: false,
    locked: false,
    createdAt: 0,
    layout: { type: 'leaf', pane: { id: `pane-${id}`, content: { kind: 'new-tab' } } },
  }
}

function workspace(overrides: Partial<Workspace> & { id: string }): Workspace {
  return {
    name: overrides.id,
    tabs: [],
    activeTabId: null,
    ...overrides,
  }
}

/** Fully-consistent baseline snapshot: two tabs, one workspace holding both. */
function validSnapshot(): WorkspaceSnapshot {
  return {
    version: 1,
    capturedAt: 0,
    tabs: { t1: tab('t1'), t2: tab('t2') },
    tabOrder: ['t1', 't2'],
    activeTabId: 't1',
    workspaces: [workspace({ id: 'ws1', tabs: ['t1', 't2'], activeTabId: 't1' })],
    activeWorkspaceId: 'ws1',
    sessionMeta: {},
  }
}

describe('validateSnapshotConsistency', () => {
  it('fully-consistent snapshot → ok:true', () => {
    expect(validateSnapshotConsistency(validSnapshot())).toEqual({ ok: true })
  })

  it('(i) workspace.tabs references a ghost tab id → ok:false', () => {
    const snap = validSnapshot()
    snap.workspaces[0].tabs = ['t1', 'ghost']
    const result = validateSnapshotConsistency(snap)
    expect(result.ok).toBe(false)
    if (!result.ok) expect(result.reason.length).toBeGreaterThan(0)
  })

  it('(ii) activeTabId is a ghost id → ok:false', () => {
    const snap = validSnapshot()
    snap.activeTabId = 'ghost'
    const result = validateSnapshotConsistency(snap)
    expect(result.ok).toBe(false)
    if (!result.ok) expect(result.reason.length).toBeGreaterThan(0)
  })

  it('(iii) workspace.activeTabId not in that workspace tabs → ok:false', () => {
    const snap = validSnapshot()
    // t2 exists as a tab but is NOT in ws1.tabs below.
    snap.workspaces[0].tabs = ['t1']
    snap.workspaces[0].activeTabId = 't2'
    const result = validateSnapshotConsistency(snap)
    expect(result.ok).toBe(false)
    if (!result.ok) expect(result.reason.length).toBeGreaterThan(0)
  })

  it('(iv) activeWorkspaceId is not any workspace id → ok:false', () => {
    const snap = validSnapshot()
    snap.activeWorkspaceId = 'ws-ghost'
    const result = validateSnapshotConsistency(snap)
    expect(result.ok).toBe(false)
    if (!result.ok) expect(result.reason.length).toBeGreaterThan(0)
  })

  it('(v) tabOrder references a ghost tab id → ok:false', () => {
    const snap = validSnapshot()
    snap.tabOrder = ['t1', 't2', 'ghost']
    const result = validateSnapshotConsistency(snap)
    expect(result.ok).toBe(false)
    if (!result.ok) expect(result.reason.length).toBeGreaterThan(0)
  })

  it('null activeTabId / activeWorkspaceId / workspace.activeTabId are allowed → ok:true', () => {
    const snap = validSnapshot()
    snap.activeTabId = null
    snap.activeWorkspaceId = null
    snap.workspaces[0].activeTabId = null
    expect(validateSnapshotConsistency(snap)).toEqual({ ok: true })
  })

  it('pane settings.scope.workspaceId to a non-existent workspace does NOT fail (R3 C: only navigation refs validated)', () => {
    // R3 C — scope decision: pane-content semantic refs (settings scope) are
    // explicitly OUT of scope. A snapshot whose navigation refs are all valid
    // stays ok:true regardless of any dangling scope reference embedded in a
    // pane's content. Expressed as a valid snapshot: the point is that a
    // non-navigation ref never affects the verdict.
    const snap = validSnapshot()
    snap.tabs.t1.layout = {
      type: 'leaf',
      pane: { id: 'pane-t1', content: { kind: 'settings', scope: { workspaceId: 'ws-does-not-exist' } } },
    }
    expect(validateSnapshotConsistency(snap)).toEqual({ ok: true })
  })
})

describe('replaceTabSnapshot / replaceTabState', () => {
  // Seeded "OLD" world, deliberately disjoint from validSnapshot() ("NEW") so
  // every assertion can tell restored-old apart from applied-new.
  const seedTabs = (): Record<string, Tab> => ({ old1: tab('old1') })
  const seedWorkspaces = (): Workspace[] => [
    workspace({ id: 'oldws', tabs: ['old1'], activeTabId: 'old1' }),
  ]

  function readTab() {
    const s = useTabStore.getState()
    return { tabs: s.tabs, tabOrder: s.tabOrder, activeTabId: s.activeTabId, visitHistory: s.visitHistory }
  }
  function readWs() {
    const s = useWorkspaceStore.getState()
    return { workspaces: s.workspaces, activeWorkspaceId: s.activeWorkspaceId }
  }

  beforeEach(() => {
    // Merge-mode setState, explicitly listing every mutable field we rely on so
    // state cannot leak across tests.
    useTabStore.setState({
      tabs: seedTabs(),
      tabOrder: ['old1'],
      activeTabId: 'old1',
      visitHistory: ['old1'],
    })
    useWorkspaceStore.setState({
      workspaces: seedWorkspaces(),
      activeWorkspaceId: 'oldws',
    })
  })

  afterEach(() => {
    vi.restoreAllMocks()
    // Reset to clean defaults so no state bleeds into other suites.
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
  })

  it('1. valid snapshot → both stores equal snapshot; stale visitHistory ids dropped', () => {
    // Current visitHistory has a live id (t2, in the new tabOrder) and a stale
    // id (old1, gone from the new tab set). After replace only t2 survives.
    useTabStore.setState({ visitHistory: ['t2', 'old1'] })
    const snap = validSnapshot()

    replaceTabSnapshot(snap)

    const t = readTab()
    expect(t.tabs).toEqual(snap.tabs)
    expect(t.tabOrder).toEqual(['t1', 't2'])
    expect(t.activeTabId).toBe('t1')
    expect(t.visitHistory).toEqual(['t2']) // old1 dropped, t2 kept

    const w = readWs()
    expect(w.workspaces).toEqual(snap.workspaces)
    expect(w.activeWorkspaceId).toBe('ws1')
  })

  it('2. malformed snapshot → throws and BOTH stores completely unchanged', () => {
    const snap = validSnapshot()
    snap.activeTabId = 'ghost' // fails validate check 2

    expect(() => replaceTabSnapshot(snap)).toThrow()

    const t = readTab()
    expect(t.tabs).toEqual(seedTabs())
    expect(t.tabOrder).toEqual(['old1'])
    expect(t.activeTabId).toBe('old1')
    expect(t.visitHistory).toEqual(['old1'])

    const w = readWs()
    expect(w.workspaces).toEqual(seedWorkspaces())
    expect(w.activeWorkspaceId).toBe('oldws')
  })

  it('3. second setState (workspace) throws → BOTH stores roll back to old values', () => {
    // Throw only on the replace call; the rollback call proceeds via the spy's
    // call-through default.
    vi.spyOn(useWorkspaceStore, 'setState').mockImplementationOnce(() => {
      throw new Error('boom-workspace')
    })

    expect(() => replaceTabSnapshot(validSnapshot())).toThrow('boom-workspace')

    const t = readTab()
    expect(t.tabs).toEqual(seedTabs())
    expect(t.tabOrder).toEqual(['old1'])
    expect(t.activeTabId).toBe('old1')
    expect(t.visitHistory).toEqual(['old1'])

    const w = readWs()
    expect(w.workspaces).toEqual(seedWorkspaces())
    expect(w.activeWorkspaceId).toBe('oldws')
  })

  it('4. first setState (tab) throws → workspace never replaced, tab rolled back, no half-apply', () => {
    const tabSpy = vi
      .spyOn(useTabStore, 'setState')
      .mockImplementationOnce(() => {
        throw new Error('boom-tab')
      })
    const wsSpy = vi.spyOn(useWorkspaceStore, 'setState')

    expect(() => replaceTabSnapshot(validSnapshot())).toThrow('boom-tab')

    // Workspace store was never given the NEW values (activeWorkspaceId 'ws1').
    expect(wsSpy).not.toHaveBeenCalledWith(
      expect.objectContaining({ activeWorkspaceId: 'ws1' }),
    )
    // Workspace store remains the seeded OLD world.
    const w = readWs()
    expect(w.workspaces).toEqual(seedWorkspaces())
    expect(w.activeWorkspaceId).toBe('oldws')

    // Tab store rolled back to seeded OLD world (rollback setState is the spy's
    // second call, which calls through).
    expect(tabSpy).toHaveBeenCalled()
    const t = readTab()
    expect(t.tabs).toEqual(seedTabs())
    expect(t.tabOrder).toEqual(['old1'])
    expect(t.activeTabId).toBe('old1')
    expect(t.visitHistory).toEqual(['old1'])
  })

  it('5. replaceTabState mutates ONLY the tab store; workspace store untouched', () => {
    // visitHistory keeps ids present in the new tabOrder (n1), drops the rest.
    useTabStore.setState({ visitHistory: ['n1', 'old1'] })

    const newTabs: Record<string, Tab> = { n1: tab('n1') }
    replaceTabState(newTabs, ['n1'], 'n1')

    const t = readTab()
    expect(t.tabs).toEqual(newTabs)
    expect(t.tabOrder).toEqual(['n1'])
    expect(t.activeTabId).toBe('n1')
    expect(t.visitHistory).toEqual(['n1']) // old1 dropped

    // Workspace store completely untouched.
    const w = readWs()
    expect(w.workspaces).toEqual(seedWorkspaces())
    expect(w.activeWorkspaceId).toBe('oldws')
  })
})

function tmuxTab(
  id: string,
  hostId: string,
  sessionCode: string,
  overrides?: Partial<Extract<PaneContent, { kind: 'tmux-session' }>>,
): Tab {
  return {
    id,
    pinned: false,
    locked: false,
    createdAt: 0,
    layout: tmuxPane(`pane-${id}`, hostId, sessionCode, overrides),
  }
}

function paneOf(t: Tab): Extract<PaneContent, { kind: 'tmux-session' }> {
  return tmux((t.layout as { pane: { content: PaneContent } }).pane.content)
}

describe('T7 orchestration', () => {
  const resetStores = (): void => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
    useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
  }

  beforeEach(() => {
    localStorage.clear()
    vi.mocked(listSessions).mockReset()
    vi.mocked(createSession).mockReset()
    resetStores()
  })

  afterEach(() => {
    vi.restoreAllMocks()
    resetStores()
    localStorage.clear()
  })

  it('1. rebuildAllSessions rebuilds ALL restorable-dead incl orphans; no -prev written', async () => {
    const sessionMeta: Record<string, Record<string, SessionMeta>> = {
      hostA: {
        s1: meta({ hostId: 'hostA', sessionCode: 's1', cwd: '/a' }),
        s2: meta({ hostId: 'hostA', sessionCode: 's2', cwd: '/b' }),
        s3: meta({ hostId: 'hostA', sessionCode: 's3', cwd: '/c' }),
        s4: meta({ hostId: 'hostA', sessionCode: 's4', cwd: '/d' }), // orphan (no tab)
        s5: meta({ hostId: 'hostA', sessionCode: 's5', cwd: '/e' }), // orphan (no tab)
      },
    }
    // Current tabs reference s1..s3, each already marked terminated.
    useTabStore.setState({
      tabs: {
        t1: tmuxTab('t1', 'hostA', 's1', { terminated: 'session-closed' }),
        t2: tmuxTab('t2', 'hostA', 's2', { terminated: 'session-closed' }),
        t3: tmuxTab('t3', 'hostA', 's3', { terminated: 'session-closed' }),
      },
      tabOrder: ['t1', 't2', 't3'],
      activeTabId: 't1',
      visitHistory: [],
    })
    vi.mocked(listSessions).mockResolvedValue([])
    vi.mocked(createSession).mockImplementation(async (_h, name) => session({ code: `new-${name}`, name }))

    const report = await rebuildAllSessions({ ...validSnapshot(), sessionMeta })

    // All 5 restorable-dead sessions rebuilt (including the 2 orphans).
    expect(createSession).toHaveBeenCalledTimes(5)
    expect(report.rebuilt).toBe(5)
    expect(report.rebuiltButUnattached).toEqual([])

    const s = useTabStore.getState()
    // Structure unchanged; only terminated panes adopt the new codes.
    expect(s.tabOrder).toEqual(['t1', 't2', 't3'])
    expect(paneOf(s.tabs.t1).sessionCode).toBe('new-s1')
    expect(paneOf(s.tabs.t1).terminated).toBeUndefined()
    expect(paneOf(s.tabs.t3).sessionCode).toBe('new-s3')

    // rebuildAllSessions must NOT write the -prev backup.
    expect(readPrevSnapshot()).toBeNull()
  })

  it('2. restoreTabLayout: dead terminated (no createSession), live reattached; -prev pre-mutation; stores = snapshot', async () => {
    // OLD world uses new-tab panes so the -prev capture makes no listSessions call.
    useTabStore.setState({ tabs: { oldT: tab('oldT') }, tabOrder: ['oldT'], activeTabId: 'oldT', visitHistory: [] })
    useWorkspaceStore.setState({
      workspaces: [workspace({ id: 'oldws', tabs: ['oldT'], activeTabId: 'oldT' })],
      activeWorkspaceId: 'oldws',
    })

    const snap: WorkspaceSnapshot = {
      version: 1,
      capturedAt: 0,
      tabs: { tl: tmuxTab('tl', 'hostA', 'live1'), td: tmuxTab('td', 'hostA', 'dead1') },
      tabOrder: ['tl', 'td'],
      activeTabId: 'tl',
      workspaces: [workspace({ id: 'ws1', tabs: ['tl', 'td'], activeTabId: 'tl' })],
      activeWorkspaceId: 'ws1',
      sessionMeta: {
        hostA: {
          live1: meta({ hostId: 'hostA', sessionCode: 'live1' }),
          dead1: meta({ hostId: 'hostA', sessionCode: 'dead1' }),
        },
      },
    }
    vi.mocked(listSessions).mockResolvedValue([session({ code: 'live1', name: 'live1' })])

    await restoreTabLayout(snap, { now: 123 })

    expect(createSession).not.toHaveBeenCalled()
    const s = useTabStore.getState()
    expect(s.tabOrder).toEqual(['tl', 'td'])
    expect(paneOf(s.tabs.tl).sessionCode).toBe('live1')
    expect(paneOf(s.tabs.tl).terminated).toBeUndefined()
    expect(paneOf(s.tabs.td).terminated).toBe('tmux-restarted')

    const w = useWorkspaceStore.getState()
    expect(w.workspaces).toEqual(snap.workspaces)
    expect(w.activeWorkspaceId).toBe('ws1')

    // -prev captured the OLD world → it was built BEFORE the store mutation.
    const prev = readPrevSnapshot()
    expect(prev).not.toBeNull()
    expect(Object.keys(prev!.tabs)).toEqual(['oldT'])
  })

  it('3. restoreAll: full rebuild + replace = snapshot; -prev written', async () => {
    useTabStore.setState({ tabs: { oldT: tab('oldT') }, tabOrder: ['oldT'], activeTabId: 'oldT', visitHistory: [] })
    useWorkspaceStore.setState({
      workspaces: [workspace({ id: 'oldws', tabs: ['oldT'], activeTabId: 'oldT' })],
      activeWorkspaceId: 'oldws',
    })

    const snap: WorkspaceSnapshot = {
      version: 1,
      capturedAt: 0,
      tabs: { tl: tmuxTab('tl', 'hostA', 'live1'), td: tmuxTab('td', 'hostA', 'dead1') },
      tabOrder: ['tl', 'td'],
      activeTabId: 'tl',
      workspaces: [workspace({ id: 'ws1', tabs: ['tl', 'td'], activeTabId: 'tl' })],
      activeWorkspaceId: 'ws1',
      sessionMeta: {
        hostA: {
          live1: meta({ hostId: 'hostA', sessionCode: 'live1' }),
          dead1: meta({ hostId: 'hostA', sessionCode: 'dead1', name: 'd1', cwd: '/w' }),
        },
      },
    }
    vi.mocked(listSessions).mockResolvedValue([session({ code: 'live1', name: 'live1' })])
    vi.mocked(createSession).mockImplementation(async (_h, name) => session({ code: `new-${name}`, name }))

    await restoreAll(snap, { now: 999 })

    expect(createSession).toHaveBeenCalledTimes(1)
    const s = useTabStore.getState()
    expect(paneOf(s.tabs.tl).sessionCode).toBe('live1')
    expect(paneOf(s.tabs.td).sessionCode).toBe('new-d1')
    expect(paneOf(s.tabs.td).terminated).toBeUndefined()
    expect(useWorkspaceStore.getState().workspaces).toEqual(snap.workspaces)
    expect(readPrevSnapshot()).not.toBeNull()
  })

  it('4. undoLastRestore restores the pre-restore world captured in -prev', async () => {
    // pre-A world (new-tab panes → deterministic buildSnapshot, no listSessions).
    useTabStore.setState({ tabs: { preA: tab('preA') }, tabOrder: ['preA'], activeTabId: 'preA', visitHistory: [] })
    useWorkspaceStore.setState({
      workspaces: [workspace({ id: 'wsPre', tabs: ['preA'], activeTabId: 'preA' })],
      activeWorkspaceId: 'wsPre',
    })
    vi.mocked(listSessions).mockResolvedValue([])

    const A = validSnapshot() // t1/t2, ws1, empty sessionMeta
    await restoreAll(A, { now: 1 })
    expect(Object.keys(useTabStore.getState().tabs).sort()).toEqual(['t1', 't2'])

    const undone = await undoLastRestore({ now: 2 })
    expect(undone).not.toBeNull()
    expect(Object.keys(useTabStore.getState().tabs)).toEqual(['preA'])
    expect(useTabStore.getState().tabOrder).toEqual(['preA'])
    expect(useWorkspaceStore.getState().workspaces[0].id).toBe('wsPre')
  })

  it('4b. undoLastRestore with no -prev present returns null', async () => {
    expect(readPrevSnapshot()).toBeNull()
    expect(await undoLastRestore()).toBeNull()
  })

  it('5. restoreAll rejects with RestoreError listing rebuiltButUnattached when replace fails; stores unchanged', async () => {
    useTabStore.setState({ tabs: { oldT: tab('oldT') }, tabOrder: ['oldT'], activeTabId: 'oldT', visitHistory: [] })
    useWorkspaceStore.setState({
      workspaces: [workspace({ id: 'oldws', tabs: ['oldT'], activeTabId: 'oldT' })],
      activeWorkspaceId: 'oldws',
    })

    const snap: WorkspaceSnapshot = {
      version: 1,
      capturedAt: 0,
      tabs: { td: tmuxTab('td', 'hostA', 'dead1') },
      tabOrder: ['td'],
      activeTabId: 'ghost', // navigation ref fails validation → replaceTabSnapshot throws
      workspaces: [workspace({ id: 'ws1', tabs: ['td'], activeTabId: 'td' })],
      activeWorkspaceId: 'ws1',
      sessionMeta: { hostA: { dead1: meta({ hostId: 'hostA', sessionCode: 'dead1', name: 'd1', cwd: '/w' }) } },
    }
    vi.mocked(listSessions).mockResolvedValue([])
    vi.mocked(createSession).mockResolvedValue(session({ code: 'new-d1', name: 'd1', cwd: '/w' }))

    let err: unknown
    await restoreAll(snap, { now: 5 }).catch((e) => {
      err = e
    })

    expect(err).toBeInstanceOf(RestoreError)
    expect((err as RestoreError).report.rebuiltButUnattached).toEqual([
      { hostId: 'hostA', name: 'd1', cwd: '/w' },
    ])
    // Daemon session was created but the front-end stores are rolled back (T6).
    expect(useTabStore.getState().tabOrder).toEqual(['oldT'])
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe('oldws')
  })

  it('6. syncSessionStore places rebuilt/reattached sessions into the session store', () => {
    const remap: Remap = {
      hostA: {
        dead1: { status: 'rebuilt', newCode: 'new1', session: session({ code: 'new1', name: 'N1' }) },
      },
    }
    syncSessionStore(remap)
    const sessions = useSessionStore.getState().sessions.hostA
    expect(sessions).toHaveLength(1)
    expect(sessions[0].code).toBe('new1')
  })

  it('7. syncSessionStore aggregates per host: ONE replaceHost call carrying BOTH sessions (no clobber)', () => {
    const remap: Remap = {
      hostA: {
        a: { status: 'reattached', newCode: 'a', session: session({ code: 'a', name: 'A' }) },
        b: { status: 'rebuilt', newCode: 'b', session: session({ code: 'b', name: 'B' }) },
      },
    }
    const spy = vi.spyOn(useSessionStore.getState(), 'replaceHost')

    syncSessionStore(remap)

    const callsForHostA = spy.mock.calls.filter((c) => c[0] === 'hostA')
    expect(callsForHostA).toHaveLength(1)
    expect(callsForHostA[0][1]).toHaveLength(2)
    expect(useSessionStore.getState().sessions.hostA).toHaveLength(2)
  })

  it('8. syncSessionStore preserves host live sessions that are NOT in the snapshot remap', () => {
    // hostA already has an unrelated live session opened outside this snapshot.
    useSessionStore.setState({
      sessions: { hostA: [session({ code: 'extra', name: 'Extra' })] },
    })
    const remap: Remap = {
      hostA: {
        dead1: { status: 'rebuilt', newCode: 'new1', session: session({ code: 'new1', name: 'N1' }) },
      },
    }

    syncSessionStore(remap)

    const sessions = useSessionStore.getState().sessions.hostA
    const codes = sessions.map((s) => s.code).sort()
    // 'extra' must survive; the rebuilt 'new1' is upserted alongside it.
    expect(codes).toEqual(['extra', 'new1'])
  })

  it('9. syncSessionStore drops the snapshot old code on rebuild (no ghost), keeping unrelated live sessions', () => {
    // hostA store holds the OLD (now-dead) code plus an unrelated live session.
    useSessionStore.setState({
      sessions: {
        hostA: [session({ code: 'old', name: 'Old' }), session({ code: 'extra', name: 'Extra' })],
      },
    })
    const remap: Remap = {
      hostA: {
        old: { status: 'rebuilt', newCode: 'new', session: session({ code: 'new', name: 'New' }) },
      },
    }

    syncSessionStore(remap)

    const codes = useSessionStore.getState().sessions.hostA.map((s) => s.code).sort()
    // 'old' must be gone (dead, replaced by 'new'); 'extra' preserved; 'new' added.
    expect(codes).toEqual(['extra', 'new'])
  })

  it('10. restoreAll discloses rebuiltButUnattached when the -prev backup write fails; stores unchanged', async () => {
    useTabStore.setState({ tabs: { oldT: tab('oldT') }, tabOrder: ['oldT'], activeTabId: 'oldT', visitHistory: [] })
    useWorkspaceStore.setState({
      workspaces: [workspace({ id: 'oldws', tabs: ['oldT'], activeTabId: 'oldT' })],
      activeWorkspaceId: 'oldws',
    })

    // Snapshot has VALID navigation refs, so only the backup write can fail here.
    const snap: WorkspaceSnapshot = {
      version: 1,
      capturedAt: 0,
      tabs: { td: tmuxTab('td', 'hostA', 'dead1') },
      tabOrder: ['td'],
      activeTabId: 'td',
      workspaces: [workspace({ id: 'ws1', tabs: ['td'], activeTabId: 'td' })],
      activeWorkspaceId: 'ws1',
      sessionMeta: { hostA: { dead1: meta({ hostId: 'hostA', sessionCode: 'dead1', name: 'd1', cwd: '/w' }) } },
    }
    vi.mocked(listSessions).mockResolvedValue([])
    vi.mocked(createSession).mockResolvedValue(session({ code: 'new-d1', name: 'd1', cwd: '/w' }))

    let err: unknown
    await restoreAll(snap, {
      now: 5,
      buildSnapshotFn: () => Promise.reject(new Error('quota exceeded')),
    }).catch((e) => {
      err = e
    })

    expect(err).toBeInstanceOf(RestoreError)
    expect((err as RestoreError).report.rebuiltButUnattached).toEqual([
      { hostId: 'hostA', name: 'd1', cwd: '/w' },
    ])
    // Backup failed before any store mutation → stores untouched.
    expect(useTabStore.getState().tabOrder).toEqual(['oldT'])
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe('oldws')
  })
})

// ---------------------------------------------------------------------------
// The shared operation lock (spec §4.11). All four legacy actions create
// sessions and/or replace the whole tab snapshot, so none of them may run
// while a Tab Rebuild is in flight — and vice versa.
// ---------------------------------------------------------------------------
describe('legacy snapshot actions — operation lock', () => {
  const resetStores = (): void => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
    useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
    useRebuildStore.setState({ operations: {}, lockedBy: null })
  }

  beforeEach(() => {
    localStorage.clear()
    vi.mocked(listSessions).mockReset()
    vi.mocked(createSession).mockReset()
    vi.mocked(listSessions).mockResolvedValue([])
    resetStores()
  })

  afterEach(() => {
    vi.restoreAllMocks()
    resetStores()
    localStorage.clear()
  })

  const emptySnapshot = (): WorkspaceSnapshot => ({
    version: 1,
    capturedAt: 0,
    tabs: {},
    tabOrder: [],
    activeTabId: null,
    workspaces: [],
    activeWorkspaceId: null,
    sessionMeta: {},
  })

  it.each([
    ['rebuildAllSessions', () => rebuildAllSessions(emptySnapshot())],
    ['restoreTabLayout', () => restoreTabLayout(emptySnapshot(), { now: 1 })],
    ['restoreAll', () => restoreAll(emptySnapshot(), { now: 1 })],
  ])('%s refuses to run while a rebuild holds the lock', async (_name, run) => {
    useRebuildStore.getState().acquireOperationLock('rebuild:p1')
    await expect(run()).rejects.toThrow(/rebuild:p1/)
    expect(createSession).not.toHaveBeenCalled()
    expect(useRebuildStore.getState().lockedBy).toBe('rebuild:p1')
  })

  it('undoLastRestore refuses to run while a rebuild holds the lock', async () => {
    await restoreAll(emptySnapshot(), { now: 1 })
    expect(readPrevSnapshot()).not.toBeNull()
    useRebuildStore.getState().acquireOperationLock('rebuild:p1')
    await expect(undoLastRestore({ now: 2 })).rejects.toThrow(/rebuild:p1/)
    expect(useRebuildStore.getState().lockedBy).toBe('rebuild:p1')
  })

  it('holds the lock for the duration of restoreAll and releases it afterwards', async () => {
    let heldDuringBuild: string | null = null
    await restoreAll(emptySnapshot(), {
      now: 1,
      buildSnapshotFn: async (now: number) => {
        heldDuringBuild = useRebuildStore.getState().lockedBy
        return { ...emptySnapshot(), capturedAt: now }
      },
    })
    expect(heldDuringBuild).toBe('snapshot:restoreAll')
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })

  it('releases the lock when a restore rejects with RestoreError', async () => {
    await restoreAll(emptySnapshot(), {
      now: 1,
      buildSnapshotFn: () => Promise.reject(new Error('quota exceeded')),
    }).catch(() => {})
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })

  it('undoLastRestore nests into restoreAll under one owner and unlocks at the end', async () => {
    useTabStore.setState({ tabs: { preA: tab('preA') }, tabOrder: ['preA'], activeTabId: 'preA', visitHistory: [] })
    useWorkspaceStore.setState({
      workspaces: [workspace({ id: 'wsPre', tabs: ['preA'], activeTabId: 'preA' })],
      activeWorkspaceId: 'wsPre',
    })
    await restoreAll(validSnapshot(), { now: 1 })
    expect(useRebuildStore.getState().lockedBy).toBeNull()

    const seen: (string | null)[] = []
    const undone = await undoLastRestore({
      now: 2,
      buildSnapshotFn: async (now: number) => {
        seen.push(useRebuildStore.getState().lockedBy)
        return { ...emptySnapshot(), capturedAt: now }
      },
    })
    // The nested restoreAll runs under the undo's own owner and its release is
    // a no-op, so the lock survives until undoLastRestore itself returns.
    expect(undone).not.toBeNull()
    expect(seen).toEqual(['snapshot:undoLastRestore'])
    expect(useRebuildStore.getState().lockedBy).toBeNull()
    expect(Object.keys(useTabStore.getState().tabs)).toEqual(['preA'])
  })
})
