// spa/src/lib/rebuild/provenance-probe.test.ts — "who owns this pane?" (§5.4).
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { probeSessionProvenance, resetProvenanceProbes } from './provenance-probe'
import { useTabStore } from '../../stores/useTabStore'
import { useHostStore } from '../../stores/useHostStore'
import { createTab, type TmuxSessionContent } from '../../types/tab'
import { fetchSessionProvenance, type SessionProvenance } from '../host-api'

vi.mock('../host-api', () => ({ fetchSessionProvenance: vi.fn() }))

/** The daemon's ownership answer, with the generation it was sampled in. */
function answer(over?: Partial<SessionProvenance>): SessionProvenance {
  return {
    found: true,
    agentType: 'cc',
    sessionId: 'sess-1',
    cwd: '/w/proj',
    tmuxPaneId: '%12',
    tmuxInstance: '222:2000',
    lastSeenAt: 1788800000000,
    ...over,
  }
}

const notFound = (tmuxInstance = '222:2000'): SessionProvenance => ({
  found: false, agentType: '', sessionId: '', cwd: '', tmuxPaneId: '',
  tmuxInstance, lastSeenAt: 0,
})

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
  let resolve!: (v: SessionProvenance) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<SessionProvenance>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

/** The attach gate for a host, which the probe waits on (spec §5.4). */
function setGate(hostId: string, open: boolean) {
  useHostStore.setState({
    runtime: {
      ...useHostStore.getState().runtime,
      [hostId]: { status: 'connected', attachReady: open },
    },
  })
}

/** Give the pane an agent record, optionally flagged `unverified`. */
function giveAgent(type: string, sessionId: string, unverified?: boolean) {
  useTabStore.getState().setPaneRebuild('h1', 'abc123', '222:2000', {
    kind: 'agent-group',
    record: {
      tmuxInstance: '222:2000',
      agent: { type, sessionId, updatedAt: 1 },
      capturedAt: 1,
    },
  })
  if (unverified) {
    useTabStore.getState().setPaneRebuild('h1', 'abc123', '222:2000', {
      kind: 'unverified', unverified: true,
    })
  }
}

beforeEach(() => {
  resetProvenanceProbes()
  vi.mocked(fetchSessionProvenance).mockReset()
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  useHostStore.setState({ runtime: {} })
  setGate('h1', true)
})

describe('probeSessionProvenance', () => {
  // --- The positive case: the answer becomes the record ---

  it('maps every field of a matching answer onto the agent-backfill patch', async () => {
    const tab = seed()
    vi.mocked(fetchSessionProvenance).mockResolvedValue(answer())
    probeSessionProvenance('h1', 'abc123', '222:2000')
    await vi.waitFor(() => expect(recordOf(tab.id)?.agent).toBeDefined())
    expect(recordOf(tab.id)?.agent).toEqual({
      type: 'cc', sessionId: 'sess-1', tmuxPaneId: '%12', updatedAt: 1788800000000,
    })
    expect(recordOf(tab.id)?.cwd).toBe('/w/proj')
    expect(recordOf(tab.id)?.cwdSource).toBe('agent-backfill')
    expect(recordOf(tab.id)?.resumeCommand).toBe('claude --resume sess-1')
  })

  it('writes nothing when the daemon found no owner', async () => {
    const tab = seed()
    vi.mocked(fetchSessionProvenance).mockResolvedValue(notFound())
    probeSessionProvenance('h1', 'abc123', '222:2000')
    await flush()
    expect(fetchSessionProvenance).toHaveBeenCalledTimes(1)
    expect(recordOf(tab.id)?.agent).toBeUndefined()
  })

  // --- Who asks ---

  it('does not ask while the host attach gate is closed', () => {
    seed()
    setGate('h1', false)
    probeSessionProvenance('h1', 'abc123', '222:2000')
    expect(fetchSessionProvenance).not.toHaveBeenCalled()
  })

  it('does not ask with an empty host id or session code', () => {
    seed()
    probeSessionProvenance('', 'abc123', '222:2000')
    probeSessionProvenance('h1', '', '222:2000')
    expect(fetchSessionProvenance).not.toHaveBeenCalled()
  })

  it('never asks for a pane whose agent is present and verified', () => {
    seed()
    giveAgent('cc', 'sess-1')
    probeSessionProvenance('h1', 'abc123', '222:2000')
    expect(fetchSessionProvenance).not.toHaveBeenCalled()
  })

  it('asks for a pane flagged unverified even though it has an agent', async () => {
    const tab = seed()
    giveAgent('cc', 'sess-old', true)
    vi.mocked(fetchSessionProvenance).mockResolvedValue(answer({ sessionId: 'sess-old' }))
    probeSessionProvenance('h1', 'abc123', '222:2000')
    expect(fetchSessionProvenance).toHaveBeenCalledTimes(1)
    await vi.waitFor(() => expect(recordOf(tab.id)?.unverified).toBeUndefined())
  })

  it('ignores stream-mode, terminated and foreign-generation panes', () => {
    seed({ mode: 'stream' })
    probeSessionProvenance('h1', 'abc123', '222:2000')
    seed({ terminated: 'session-closed' })
    probeSessionProvenance('h1', 'abc123', '222:2000')
    seed({ tmuxInstance: '111:1000' })
    probeSessionProvenance('h1', 'abc123', '222:2000')
    expect(fetchSessionProvenance).not.toHaveBeenCalled()
  })

  it('deduplicates concurrent probes for the same binding', async () => {
    const tab = seed()
    const d = deferred()
    vi.mocked(fetchSessionProvenance).mockReturnValue(d.promise)
    probeSessionProvenance('h1', 'abc123', '222:2000')
    probeSessionProvenance('h1', 'abc123', '222:2000')
    probeSessionProvenance('h1', 'abc123', '222:2000')
    expect(fetchSessionProvenance).toHaveBeenCalledTimes(1)
    d.resolve(answer())
    await vi.waitFor(() => expect(recordOf(tab.id)?.agent?.type).toBe('cc'))
  })

  it('discards an answer whose pane was re-pointed while it was in flight', async () => {
    const tab = seed()
    const d = deferred()
    vi.mocked(fetchSessionProvenance).mockReturnValue(d.promise)
    probeSessionProvenance('h1', 'abc123', '222:2000')
    rebind(tab.id, 'other-code')
    d.resolve(answer())
    await flush()
    expect(recordOf(tab.id)?.agent).toBeUndefined()
  })

  it('swallows a fetch rejection and releases the dedup slot', async () => {
    seed()
    const d = deferred()
    vi.mocked(fetchSessionProvenance).mockReturnValueOnce(d.promise)
    probeSessionProvenance('h1', 'abc123', '222:2000')
    d.reject(new Error('boom'))
    await flush()
    vi.mocked(fetchSessionProvenance).mockResolvedValue(answer())
    probeSessionProvenance('h1', 'abc123', '222:2000')
    expect(fetchSessionProvenance).toHaveBeenCalledTimes(2)
  })

  // --- The two comparisons that look alike and are not (§5.4) ---

  it('disowns a binding only when requested and answered generations are both non-empty and different', async () => {
    const tab = seed()
    vi.mocked(fetchSessionProvenance).mockResolvedValue(answer({ tmuxInstance: '333:3000' }))
    probeSessionProvenance('h1', 'abc123', '222:2000')
    await flush()
    expect(recordOf(tab.id)?.agent).toBeUndefined()

    // Conclusive: the code belongs to a stranger, so nothing asks again.
    probeSessionProvenance('h1', 'abc123', '222:2000')
    await flush()
    expect(fetchSessionProvenance).toHaveBeenCalledTimes(1)
  })

  it('blocks the write on an answered empty generation without disowning the binding', async () => {
    const tab = seed()
    vi.mocked(fetchSessionProvenance).mockResolvedValueOnce(answer({ tmuxInstance: '' }))
    probeSessionProvenance('h1', 'abc123', '222:2000')
    await flush()
    expect(recordOf(tab.id)?.agent).toBeUndefined()

    // '' is the daemon's "I could not tell", so a later attempt still runs.
    vi.mocked(fetchSessionProvenance).mockResolvedValue(answer())
    probeSessionProvenance('h1', 'abc123', '222:2000')
    await vi.waitFor(() => expect(recordOf(tab.id)?.agent?.sessionId).toBe('sess-1'))
    expect(fetchSessionProvenance).toHaveBeenCalledTimes(2)
  })

  it('blocks the write when the pane asked with an empty generation, and stays retryable', async () => {
    // The pane is ELIGIBLE (generationMatchesLegacy is one-way: a recorded ''
    // matches anything), but an unknown requested generation authorises no
    // write and is no proof the code was reused.
    const tab = seed({ tmuxInstance: '' })
    vi.mocked(fetchSessionProvenance).mockResolvedValueOnce(answer())
    probeSessionProvenance('h1', 'abc123', '')
    await flush()
    expect(fetchSessionProvenance).toHaveBeenCalledTimes(1)
    expect(recordOf(tab.id)?.agent).toBeUndefined()

    vi.mocked(fetchSessionProvenance).mockResolvedValue(answer({ tmuxInstance: '' }))
    probeSessionProvenance('h1', 'abc123', '')
    await flush()
    expect(fetchSessionProvenance).toHaveBeenCalledTimes(2)
    expect(recordOf(tab.id)?.agent).toBeUndefined()
  })
})
