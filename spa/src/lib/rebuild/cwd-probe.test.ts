// spa/src/lib/rebuild/cwd-probe.test.ts — the shell-only cwd baseline (§4.4).
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { probeSessionCwd, probeMissingCwds, resetCwdProbes } from './cwd-probe'
import { useTabStore } from '../../stores/useTabStore'
import { createTab, type TmuxSessionContent } from '../../types/tab'
import { fetchSessionCwd } from '../host-api'

vi.mock('../host-api', () => ({ fetchSessionCwd: vi.fn() }))

function seed(overrides?: Partial<TmuxSessionContent>) {
  const tab = createTab({
    kind: 'tmux-session', hostId: 'h1', sessionCode: 'abc123',
    mode: 'terminal', cachedName: 'dev', tmuxInstance: '222:2000',
    ...overrides,
  })
  useTabStore.setState({ tabs: { [tab.id]: tab }, tabOrder: [tab.id], activeTabId: tab.id })
  return tab
}

function recordOf(tabId: string) {
  const l = useTabStore.getState().tabs[tabId].layout
  return l.type === 'leaf' && l.pane.content.kind === 'tmux-session' ? l.pane.content.rebuild : undefined
}

/** Re-point the tab's single pane at another session code. */
function rebind(tabId: string, sessionCode: string) {
  const state = useTabStore.getState()
  const tab = state.tabs[tabId]
  const l = tab.layout
  if (l.type !== 'leaf' || l.pane.content.kind !== 'tmux-session') throw new Error('bad seed')
  useTabStore.setState({
    tabs: {
      ...state.tabs,
      [tabId]: { ...tab, layout: { ...l, pane: { ...l.pane, content: { ...l.pane.content, sessionCode } } } },
    },
  })
}

/** Flush every pending microtask (the probe chain is then → catch → finally). */
const flush = () => new Promise((r) => setTimeout(r, 0))

function deferred() {
  let resolve!: (v: string) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<string>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

beforeEach(() => {
  resetCwdProbes()
  vi.mocked(fetchSessionCwd).mockReset()
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
})

describe('probeSessionCwd', () => {
  it('writes the probed cwd as a pane-probe baseline', async () => {
    const tab = seed()
    vi.mocked(fetchSessionCwd).mockResolvedValue('/w/late')
    probeSessionCwd('h1', 'abc123', '222:2000')
    await vi.waitFor(() => expect(recordOf(tab.id)?.cwd).toBe('/w/late'))
    expect(recordOf(tab.id)?.cwdSource).toBe('pane-probe')
  })

  it('deduplicates concurrent probes for the same binding', async () => {
    const tab = seed()
    const d = deferred()
    vi.mocked(fetchSessionCwd).mockReturnValue(d.promise)
    probeSessionCwd('h1', 'abc123', '222:2000')
    probeSessionCwd('h1', 'abc123', '222:2000')
    probeMissingCwds('h1')
    expect(fetchSessionCwd).toHaveBeenCalledTimes(1)
    d.resolve('/w/one')
    await vi.waitFor(() => expect(recordOf(tab.id)?.cwd).toBe('/w/one'))
  })

  it('discards a probe whose pane was re-pointed while it was in flight', async () => {
    const tab = seed()
    const d = deferred()
    vi.mocked(fetchSessionCwd).mockReturnValue(d.promise)
    probeSessionCwd('h1', 'abc123', '222:2000')
    rebind(tab.id, 'other-code')
    d.resolve('/w/stale')
    await flush()
    expect(recordOf(tab.id)?.cwd).toBeUndefined()
  })

  it('never requests a cwd for a pane that already has one', () => {
    seed()
    useTabStore.getState().setPaneRebuild('h1', 'abc123', '222:2000', {
      kind: 'field', field: 'cwd', value: '/known',
    })
    probeSessionCwd('h1', 'abc123', '222:2000')
    expect(fetchSessionCwd).not.toHaveBeenCalled()
  })

  it('ignores stream-mode, terminated and foreign-generation panes', () => {
    seed({ mode: 'stream' })
    probeSessionCwd('h1', 'abc123', '222:2000')
    seed({ terminated: 'session-closed' })
    probeSessionCwd('h1', 'abc123', '222:2000')
    seed({ tmuxInstance: '111:1000' })
    probeSessionCwd('h1', 'abc123', '222:2000')
    expect(fetchSessionCwd).not.toHaveBeenCalled()
  })

  it('releases the dedup slot when the request fails', async () => {
    seed()
    const d = deferred()
    vi.mocked(fetchSessionCwd).mockReturnValueOnce(d.promise)
    probeSessionCwd('h1', 'abc123', '222:2000')
    d.reject(new Error('boom'))
    await flush()
    vi.mocked(fetchSessionCwd).mockResolvedValue('/w/retry')
    probeSessionCwd('h1', 'abc123', '222:2000')
    expect(fetchSessionCwd).toHaveBeenCalledTimes(2)
  })

  it('writes nothing when the host answers with an empty cwd', async () => {
    const tab = seed()
    vi.mocked(fetchSessionCwd).mockResolvedValue('')
    probeSessionCwd('h1', 'abc123', '222:2000')
    await flush()
    expect(fetchSessionCwd).toHaveBeenCalledTimes(1)
    expect(recordOf(tab.id)?.cwd).toBeUndefined()
  })
})

describe('probeMissingCwds', () => {
  it('probes every distinct binding on the host that has no cwd', async () => {
    const a = seed()
    const b = createTab({
      kind: 'tmux-session', hostId: 'h1', sessionCode: 'def456',
      mode: 'terminal', cachedName: 'other', tmuxInstance: '222:2000',
    })
    const foreign = createTab({
      kind: 'tmux-session', hostId: 'h2', sessionCode: 'zzz999',
      mode: 'terminal', cachedName: 'far', tmuxInstance: '222:2000',
    })
    useTabStore.setState({
      tabs: { ...useTabStore.getState().tabs, [b.id]: b, [foreign.id]: foreign },
      tabOrder: [a.id, b.id, foreign.id],
    })
    vi.mocked(fetchSessionCwd).mockImplementation(async (_h, code) => `/w/${code}`)

    probeMissingCwds('h1')

    await vi.waitFor(() => expect(recordOf(b.id)?.cwd).toBe('/w/def456'))
    expect(recordOf(a.id)?.cwd).toBe('/w/abc123')
    expect(recordOf(foreign.id)?.cwd).toBeUndefined()
    expect(fetchSessionCwd).toHaveBeenCalledTimes(2)
  })
})
