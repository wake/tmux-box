import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { listSessions, fetchSessionCwd } from '../host-api'
import type { Session } from '../host-api'
import type { Tab, PaneLayout, PaneContent } from '../../types/tab'
import { readSnapshot, writeSnapshot } from './storage'
import type { WorkspaceSnapshot } from './types'
import { buildSnapshot, captureSnapshot } from './capture'

vi.mock('../host-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../host-api')>()),
  listSessions: vi.fn(),
  fetchSessionCwd: vi.fn(),
}))

function tmuxContent(
  hostId: string,
  sessionCode: string,
  cachedName: string,
  mode: 'terminal' | 'stream' = 'terminal',
): PaneContent {
  return { kind: 'tmux-session', hostId, sessionCode, mode, cachedName, tmuxInstance: '' }
}

function leaf(paneId: string, content: PaneContent): PaneLayout {
  return { type: 'leaf', pane: { id: paneId, content } }
}

function split(id: string, children: PaneLayout[]): PaneLayout {
  const sizes = children.map(() => 100 / children.length)
  return { type: 'split', id, direction: 'h', children, sizes }
}

function tab(id: string, layout: PaneLayout): Tab {
  return { id, pinned: false, locked: false, createdAt: 0, layout }
}

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

function seedStores(tabs: Record<string, Tab>, tabOrder: string[], activeTabId: string | null): void {
  useTabStore.setState({ tabs, tabOrder, activeTabId })
  useWorkspaceStore.getState().reset()
}

/**
 * Configure fetchSessionCwd mock keyed by sessionCode. Value can be a string
 * (resolves to it) or an Error (rejects with it). Codes absent from the map
 * reject (they should never be asked — e.g. dead sessions).
 */
function mockCwds(map: Record<string, string | Error>): void {
  vi.mocked(fetchSessionCwd).mockImplementation(async (_hostId: string, code: string) => {
    const v = map[code]
    if (v instanceof Error) throw v
    if (v === undefined) throw new Error(`unexpected fetchSessionCwd for ${code}`)
    // The endpoint stamps the generation it sampled in (spec §4.6.2); capture
    // takes only the cwd.
    return { cwd: v, tmuxInstance: '222:2000' }
  })
}

describe('buildSnapshot / captureSnapshot', () => {
  beforeEach(() => {
    localStorage.clear()
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
    useWorkspaceStore.getState().reset()
    vi.mocked(listSessions).mockReset()
    vi.mocked(fetchSessionCwd).mockReset()
  })

  it('1. live session cwd comes from fetchSessionCwd (pane_current_path), not session_path', async () => {
    seedStores(
      { t1: tab('t1', leaf('p1', tmuxContent('hostA', 'code1', 'cached1'))) },
      ['t1'],
      't1',
    )
    // listSessions.cwd is the (unexpanded) session_path '~'; the real cwd differs.
    vi.mocked(listSessions).mockResolvedValue([
      session({ code: 'code1', name: 'live1', cwd: '~', current_command: 'vim' }),
    ])
    mockCwds({ code1: '/Users/wake/Workspace/x' })

    const result = await captureSnapshot(1000)

    expect(listSessions).toHaveBeenCalledTimes(1)
    expect(listSessions).toHaveBeenCalledWith('hostA')
    expect(fetchSessionCwd).toHaveBeenCalledWith('hostA', 'code1')

    const stored = readSnapshot()!
    expect(stored.capturedAt).toBe(1000)
    expect(stored.sessionMeta.hostA.code1).toEqual({
      hostId: 'hostA', sessionCode: 'code1', name: 'live1', mode: 'terminal',
      cwd: '/Users/wake/Workspace/x', currentCommand: 'vim', restorable: true,
    })
    expect(result).toEqual({ total: 1, resolved: 1, unresolved: 0 })
  })

  it('2. cross-host same sessionCode literal stays independent (composite key)', async () => {
    const tabs: Record<string, Tab> = {
      t1: tab('t1', leaf('p1', tmuxContent('hostA', 'shared', 'cachedA'))),
      t2: tab('t2', leaf('p2', tmuxContent('hostB', 'shared', 'cachedB'))),
    }
    seedStores(tabs, ['t1', 't2'], 't1')

    vi.mocked(listSessions).mockImplementation(async (hostId: string) => {
      if (hostId === 'hostA') return [session({ code: 'shared', name: 'a-name', cwd: '~' })]
      if (hostId === 'hostB') return [session({ code: 'shared', name: 'b-name', cwd: '~' })]
      throw new Error(`unexpected host ${hostId}`)
    })
    vi.mocked(fetchSessionCwd).mockImplementation(async (hostId: string, code: string) => {
      if (code !== 'shared') throw new Error(`unexpected code ${code}`)
      return { cwd: hostId === 'hostA' ? '/host-a' : '/host-b', tmuxInstance: '222:2000' }
    })

    const snap = await buildSnapshot(2000)

    expect(listSessions).toHaveBeenCalledTimes(2)
    expect(snap.sessionMeta.hostA.shared.cwd).toBe('/host-a')
    expect(snap.sessionMeta.hostB.shared.cwd).toBe('/host-b')
    expect(snap.sessionMeta.hostA.shared).not.toEqual(snap.sessionMeta.hostB.shared)
  })

  it('3. fetchSessionCwd rejection falls back to session_path; other sessions unaffected', async () => {
    const layout = split('s1', [
      leaf('p1', tmuxContent('hostA', 'code1', 'cached1')),
      leaf('p2', tmuxContent('hostA', 'code2', 'cached2')),
    ])
    seedStores({ t1: tab('t1', layout) }, ['t1'], 't1')

    vi.mocked(listSessions).mockResolvedValue([
      session({ code: 'code1', name: 'live1', cwd: '~', current_command: 'vim' }),
      session({ code: 'code2', name: 'live2', cwd: '/b', current_command: 'top' }),
    ])
    mockCwds({ code1: new Error('cwd probe failed'), code2: '/real/b' })

    const result = await captureSnapshot(3000)
    const stored = readSnapshot()!

    // code1: fetch rejected → fall back to session_path '~', still restorable,
    // but flagged degraded (cwd-probe-failed) so the fallback is observable.
    expect(stored.sessionMeta.hostA.code1).toEqual({
      hostId: 'hostA', sessionCode: 'code1', name: 'live1', mode: 'terminal',
      cwd: '~', currentCommand: 'vim', restorable: true, captureError: 'cwd-probe-failed',
    })
    // code2: unaffected by code1's failure — accurate cwd applied.
    expect(stored.sessionMeta.hostA.code2).toEqual({
      hostId: 'hostA', sessionCode: 'code2', name: 'live2', mode: 'terminal',
      cwd: '/real/b', currentCommand: 'top', restorable: true,
    })
    expect(result).toEqual({ total: 2, resolved: 2, unresolved: 0 })
  })

  it('3b. one host unreachable does not interrupt the other host', async () => {
    const tabs: Record<string, Tab> = {
      t1: tab('t1', leaf('p1', tmuxContent('hostA', 'codeA', 'cachedA'))),
      t2: tab('t2', leaf('p2', tmuxContent('hostB', 'codeB', 'cachedB'))),
    }
    seedStores(tabs, ['t1', 't2'], 't1')

    vi.mocked(listSessions).mockImplementation(async (hostId: string) => {
      if (hostId === 'hostA') return [session({ code: 'codeA', name: 'live-a', cwd: '~' })]
      throw new Error('host B unreachable')
    })
    mockCwds({ codeA: '/a' })

    const result = await captureSnapshot(3500)
    const stored = readSnapshot()!

    expect(stored.sessionMeta.hostA.codeA).toMatchObject({ restorable: true, cwd: '/a' })
    // Offline host: fetchSessionCwd never asked for its sessions.
    expect(fetchSessionCwd).not.toHaveBeenCalledWith('hostB', expect.anything())
    expect(stored.sessionMeta.hostB.codeB).toEqual({
      hostId: 'hostB', sessionCode: 'codeB', name: 'cachedB', mode: 'terminal',
      cwd: undefined, restorable: false, captureError: 'host-unreachable',
    })
    expect(result).toEqual({ total: 2, resolved: 1, unresolved: 1 })
  })

  it('4. pane code missing from live list → session-dead-at-capture, cwd never probed', async () => {
    seedStores(
      { t1: tab('t1', leaf('p1', tmuxContent('hostA', 'deadcode', 'cachedDead'))) },
      ['t1'],
      't1',
    )
    vi.mocked(listSessions).mockResolvedValue([session({ code: 'other-code' })])
    mockCwds({}) // any call is a failure

    const snap = await buildSnapshot(4000)

    expect(fetchSessionCwd).not.toHaveBeenCalled()
    expect(snap.sessionMeta.hostA.deadcode).toEqual({
      hostId: 'hostA', sessionCode: 'deadcode', name: 'cachedDead', mode: 'terminal',
      cwd: undefined, restorable: false, captureError: 'session-dead-at-capture',
    })
  })

  it('7. fetchSessionCwd resolves "" and session_path also empty → not restorable, cwd undefined', async () => {
    seedStores(
      { t1: tab('t1', leaf('p1', tmuxContent('hostA', 'code1', 'cached1'))) },
      ['t1'],
      't1',
    )
    vi.mocked(listSessions).mockResolvedValue([
      session({ code: 'code1', name: 'live-name', cwd: '', current_command: 'bash' }),
    ])
    mockCwds({ code1: '' })

    const result = await captureSnapshot(7000)
    const stored = readSnapshot()!

    expect(stored.sessionMeta.hostA.code1).toEqual({
      hostId: 'hostA', sessionCode: 'code1', name: 'live-name', mode: 'terminal',
      cwd: undefined, currentCommand: 'bash', restorable: false,
    })
    expect(stored.sessionMeta.hostA.code1.captureError).toBeUndefined()
    expect(result).toEqual({ total: 1, resolved: 0, unresolved: 1 })
  })

  it('7b. fetchSessionCwd resolves "" but session_path non-empty → falls back, restorable, flagged degraded', async () => {
    seedStores(
      { t1: tab('t1', leaf('p1', tmuxContent('hostA', 'code1', 'cached1'))) },
      ['t1'],
      't1',
    )
    vi.mocked(listSessions).mockResolvedValue([
      session({ code: 'code1', name: 'live-name', cwd: '/session/start', current_command: 'bash' }),
    ])
    mockCwds({ code1: '' })

    const stored = await buildSnapshot(7100)

    expect(stored.sessionMeta.hostA.code1).toEqual({
      hostId: 'hostA', sessionCode: 'code1', name: 'live-name', mode: 'terminal',
      cwd: '/session/start', currentCommand: 'bash', restorable: true,
      captureError: 'cwd-probe-failed',
    })
  })

  it('G1. same session referenced by two panes probes cwd exactly once, deterministic', async () => {
    // Same (hostA, code1) shown in two separate tabs → collectTmuxPanesByHost
    // yields two per-pane refs for the SAME session. Without dedup this fires
    // two concurrent fetchSessionCwd calls whose completion order is
    // nondeterministic; here we assert exactly one probe and a stable cwd.
    const tabs: Record<string, Tab> = {
      t1: tab('t1', leaf('p1', tmuxContent('hostA', 'code1', 'cached1'))),
      t2: tab('t2', leaf('p2', tmuxContent('hostA', 'code1', 'cached1'))),
    }
    seedStores(tabs, ['t1', 't2'], 't1')

    vi.mocked(listSessions).mockResolvedValue([
      session({ code: 'code1', name: 'live1', cwd: '~', current_command: 'vim' }),
    ])
    mockCwds({ code1: '/real/pane/path' })

    const snap = await buildSnapshot(8000)

    // Deduped to one probe per (host, code).
    expect(fetchSessionCwd).toHaveBeenCalledTimes(1)
    expect(fetchSessionCwd).toHaveBeenCalledWith('hostA', 'code1')
    // Deterministic: the single pane_current_path, never overwritten by a fallback.
    expect(snap.sessionMeta.hostA.code1).toEqual({
      hostId: 'hostA', sessionCode: 'code1', name: 'live1', mode: 'terminal',
      cwd: '/real/pane/path', currentCommand: 'vim', restorable: true,
    })
  })

  it('6b. two live sessions on one host both resolved independently (Promise.all)', async () => {
    const layout = split('s1', [
      leaf('p1', tmuxContent('hostA', 'code1', 'cached1')),
      leaf('p2', tmuxContent('hostA', 'code2', 'cached2')),
    ])
    seedStores({ t1: tab('t1', layout) }, ['t1'], 't1')

    vi.mocked(listSessions).mockResolvedValue([
      session({ code: 'code1', name: 'live1', cwd: '~' }),
      session({ code: 'code2', name: 'live2', cwd: '~' }),
    ])
    mockCwds({ code1: '/real/one', code2: '/real/two' })

    const snap = await buildSnapshot(6100)

    expect(fetchSessionCwd).toHaveBeenCalledTimes(2)
    expect(snap.sessionMeta.hostA.code1.cwd).toBe('/real/one')
    expect(snap.sessionMeta.hostA.code2.cwd).toBe('/real/two')
    expect(snap.sessionMeta.hostA.code1.restorable).toBe(true)
    expect(snap.sessionMeta.hostA.code2.restorable).toBe(true)
  })

  it('5. no tmux panes → total=0, sessionMeta={}, still writes a snapshot', async () => {
    seedStores(
      { t1: tab('t1', leaf('p1', { kind: 'editor', source: { type: 'local' }, filePath: '/tmp/a.md' })) },
      ['t1'],
      't1',
    )

    const result = await captureSnapshot(5000)

    expect(listSessions).not.toHaveBeenCalled()
    expect(fetchSessionCwd).not.toHaveBeenCalled()
    expect(result).toEqual({ total: 0, resolved: 0, unresolved: 0 })
    const stored = readSnapshot()
    expect(stored).not.toBeNull()
    expect(stored!.sessionMeta).toEqual({})
  })

  it('6. buildSnapshot never writes storage (B1) — only captureSnapshot writes the primary key', async () => {
    const oldSnap: WorkspaceSnapshot = {
      version: 1, capturedAt: -1, tabs: {}, tabOrder: [], activeTabId: null,
      workspaces: [], activeWorkspaceId: null, sessionMeta: {},
    }
    writeSnapshot(oldSnap)

    seedStores(
      { t1: tab('t1', leaf('p1', tmuxContent('hostA', 'code1', 'cached1'))) },
      ['t1'],
      't1',
    )
    vi.mocked(listSessions).mockResolvedValue([session({ code: 'code1', cwd: '~' })])
    mockCwds({ code1: '/x' })

    const built = await buildSnapshot(6000)
    expect(built.capturedAt).toBe(6000)
    expect(readSnapshot()).toEqual(oldSnap) // still the old snapshot — buildSnapshot did not write

    await captureSnapshot(6000)
    expect(readSnapshot()!.capturedAt).toBe(6000) // now overwritten by captureSnapshot
  })
})
