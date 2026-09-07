// spa/src/lib/rebuild/provenance-probe.test.ts — "who owns this pane?" (§5.4)
// and the defer-never-drop scheduler that rate-limits it (§5.4.1).
//
// The whole file runs on fake timers, because the scheduler's contract is
// stated in instants and "eventually" would pass against every implementation
// this test file exists to rule out.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { probeSessionProvenance, resetProvenanceProbes } from './provenance-probe'
import { useTabStore } from '../../stores/useTabStore'
import { useHostStore } from '../../stores/useHostStore'
import { createTab, type TmuxSessionContent } from '../../types/tab'
import { fetchSessionProvenance, type SessionProvenance } from '../host-api'
import { resolveResumeCommand } from './composer'
import { useResumeTemplateStore, type ResumeTemplateLookup } from '../../stores/useResumeTemplateStore'

/** The shipped templates: the store answers from `DEFAULT_RESUME_TEMPLATES`. */
const defaultTemplates: ResumeTemplateLookup = (agentType) =>
  useResumeTemplateStore.getState().getTemplates(agentType)

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

/** "I have no answer" — which leaves the pane eligible, so it will ask again. */
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

/** Rewrite the tab's single pane content in place. */
function repane(tabId: string, patch: Partial<TmuxSessionContent>) {
  const state = useTabStore.getState()
  const tab = state.tabs[tabId]
  const l = tab.layout
  if (l.type !== 'leaf' || l.pane.content.kind !== 'tmux-session') throw new Error('bad seed')
  useTabStore.setState({
    tabs: {
      ...state.tabs,
      [tabId]: { ...tab, layout: { ...l, pane: { ...l.pane, content: { ...l.pane.content, ...patch } } } },
    },
  })
}

/** Re-point the tab's single pane at another session code. */
const rebind = (tabId: string, sessionCode: string) => repane(tabId, { sessionCode })

/** Settle the probe's then → catch → finally chain without moving the clock. */
async function settle() {
  await vi.advanceTimersByTimeAsync(0)
  await vi.advanceTimersByTimeAsync(0)
}

/** Move the fake clock forward and let everything it fired settle. */
async function advance(ms: number) {
  await vi.advanceTimersByTimeAsync(ms)
  await settle()
}

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

/** The one trigger surface — every caller in Task 11b goes through this. */
const trigger = () => probeSessionProvenance('h1', 'abc123', '222:2000')

const calls = () => vi.mocked(fetchSessionProvenance).mock.calls.length

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(0)
  resetProvenanceProbes()
  vi.mocked(fetchSessionProvenance).mockReset()
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  useHostStore.setState({ runtime: {} })
  setGate('h1', true)
})

afterEach(() => {
  resetProvenanceProbes()
  vi.useRealTimers()
})

describe('probeSessionProvenance', () => {
  // --- The positive case: the answer becomes the record ---

  it('maps every field of a matching answer onto the agent-backfill patch', async () => {
    const tab = seed()
    vi.mocked(fetchSessionProvenance).mockResolvedValue(answer())
    trigger()
    await settle()
    expect(recordOf(tab.id)?.agent).toEqual({
      type: 'cc', sessionId: 'sess-1', tmuxPaneId: '%12', updatedAt: 1788800000000,
    })
    expect(recordOf(tab.id)?.cwd).toBe('/w/proj')
    expect(recordOf(tab.id)?.cwdSource).toBe('agent-backfill')
    // The answer carries an identity, never a command: the resolver composes.
    expect(resolveResumeCommand(recordOf(tab.id), defaultTemplates)).toBe('claude --resume sess-1')
  })

  it('writes nothing when the daemon found no owner', async () => {
    const tab = seed()
    vi.mocked(fetchSessionProvenance).mockResolvedValue(notFound())
    trigger()
    await settle()
    expect(calls()).toBe(1)
    expect(recordOf(tab.id)?.agent).toBeUndefined()
  })

  // --- Who asks ---

  it('does not ask while the host attach gate is closed', () => {
    seed()
    setGate('h1', false)
    trigger()
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
    trigger()
    expect(fetchSessionProvenance).not.toHaveBeenCalled()
  })

  it('asks for a pane flagged unverified even though it has an agent', async () => {
    const tab = seed()
    giveAgent('cc', 'sess-old', true)
    vi.mocked(fetchSessionProvenance).mockResolvedValue(answer({ sessionId: 'sess-old' }))
    trigger()
    expect(calls()).toBe(1)
    await settle()
    expect(recordOf(tab.id)?.unverified).toBeUndefined()
  })

  it('ignores stream-mode, terminated and foreign-generation panes', () => {
    seed({ mode: 'stream' })
    trigger()
    seed({ terminated: 'session-closed' })
    trigger()
    seed({ tmuxInstance: '111:1000' })
    trigger()
    expect(fetchSessionProvenance).not.toHaveBeenCalled()
  })

  it('deduplicates concurrent probes for the same binding', async () => {
    const tab = seed()
    const d = deferred()
    vi.mocked(fetchSessionProvenance).mockReturnValue(d.promise)
    trigger()
    trigger()
    trigger()
    expect(calls()).toBe(1)
    d.resolve(answer())
    await settle()
    expect(recordOf(tab.id)?.agent?.type).toBe('cc')
  })

  it('discards an answer whose pane was re-pointed while it was in flight', async () => {
    const tab = seed()
    const d = deferred()
    vi.mocked(fetchSessionProvenance).mockReturnValue(d.promise)
    trigger()
    rebind(tab.id, 'other-code')
    d.resolve(answer())
    await settle()
    expect(recordOf(tab.id)?.agent).toBeUndefined()
  })

  it('swallows a fetch rejection and releases the dedup slot', async () => {
    seed()
    const d = deferred()
    vi.mocked(fetchSessionProvenance).mockReturnValueOnce(d.promise)
    trigger()
    d.reject(new Error('boom'))
    await settle()
    vi.mocked(fetchSessionProvenance).mockResolvedValue(answer())
    // A failure enters the cooldown like any other completion, so the retry
    // waits for the deadline rather than being refused outright.
    await advance(30_000)
    trigger()
    expect(calls()).toBe(2)
  })

  // --- The two comparisons that look alike and are not (§5.4) ---

  it('disowns a binding only when requested and answered generations are both non-empty and different', async () => {
    const tab = seed()
    vi.mocked(fetchSessionProvenance).mockResolvedValue(answer({ tmuxInstance: '333:3000' }))
    trigger()
    await settle()
    expect(recordOf(tab.id)?.agent).toBeUndefined()

    // Conclusive: the code belongs to a stranger, so nothing asks again — not
    // even once the cooldown has expired.
    await advance(30_000)
    trigger()
    await advance(60_000)
    expect(calls()).toBe(1)
  })

  it('blocks the write on an answered empty generation without disowning the binding', async () => {
    const tab = seed()
    vi.mocked(fetchSessionProvenance).mockResolvedValueOnce(answer({ tmuxInstance: '' }))
    trigger()
    await settle()
    expect(recordOf(tab.id)?.agent).toBeUndefined()

    // '' is the daemon's "I could not tell", so a later attempt still runs.
    vi.mocked(fetchSessionProvenance).mockResolvedValue(answer())
    await advance(30_000)
    trigger()
    await settle()
    expect(calls()).toBe(2)
    expect(recordOf(tab.id)?.agent?.sessionId).toBe('sess-1')
  })

  it('blocks the write when the pane asked with an empty generation, and stays retryable', async () => {
    // The pane is ELIGIBLE (generationMatchesLegacy is one-way: a recorded ''
    // matches anything), but an unknown requested generation authorises no
    // write and is no proof the code was reused.
    const tab = seed({ tmuxInstance: '' })
    vi.mocked(fetchSessionProvenance).mockResolvedValueOnce(answer())
    probeSessionProvenance('h1', 'abc123', '')
    await settle()
    expect(calls()).toBe(1)
    expect(recordOf(tab.id)?.agent).toBeUndefined()

    vi.mocked(fetchSessionProvenance).mockResolvedValue(answer({ tmuxInstance: '' }))
    await advance(30_000)
    probeSessionProvenance('h1', 'abc123', '')
    await settle()
    expect(calls()).toBe(2)
    expect(recordOf(tab.id)?.agent).toBeUndefined()
  })
})

// ---------------------------------------------------------------------------

describe('the defer-never-drop scheduler (§5.4.1)', () => {
  /** Run one request to completion at t=0, which opens a cooldown to t=30 s. */
  async function primeCooldown() {
    vi.mocked(fetchSessionProvenance).mockResolvedValue(notFound())
    trigger()
    await settle()
    expect(calls()).toBe(1)
    expect(Date.now()).toBe(0)
  }

  it('defers the single hook of an otherwise idle session instead of dropping it', async () => {
    // The round-3 Blocker: a bare cooldown would drop the one event that
    // mattered and nothing would ever ask again.
    seed()
    await primeCooldown()

    await advance(5_000)
    trigger()
    expect(calls()).toBe(1)

    await advance(24_999)          // t = 29.999 s
    expect(calls()).toBe(1)
    await advance(1)               // t = 30.000 s
    expect(calls()).toBe(2)
  })

  it('never pushes the deadline forward when more triggers arrive', async () => {
    // A debounce would restart the clock on every hook and fire at t=50 s.
    seed()
    await primeCooldown()

    for (const t of [5_000, 5_000, 5_000, 5_000]) {   // t = 5/10/15/20 s
      await advance(t)
      trigger()
    }
    expect(calls()).toBe(1)

    await advance(9_999)           // t = 29.999 s
    expect(calls()).toBe(1)
    await advance(1)               // t = 30.000 s
    expect(calls()).toBe(2)

    await advance(20_000)          // t = 50 s — the debounce deadline
    expect(calls()).toBe(2)
  })

  it('holds the boundary at t=29.999 s', async () => {
    seed()
    await primeCooldown()
    await advance(1_000)
    trigger()
    await advance(28_999)          // t = 29.999 s
    expect(calls()).toBe(1)
  })

  it('coalesces ten hooks in one cooldown into a single request', async () => {
    seed()
    await primeCooldown()

    await advance(1_000)
    for (let i = 0; i < 10; i++) {
      trigger()
      await advance(1_000)         // t = 2 s … 11 s
    }
    expect(calls()).toBe(1)

    await advance(18_999)          // t = 29.999 s
    expect(calls()).toBe(1)
    await advance(1)               // t = 30.000 s
    expect(calls()).toBe(2)
    await advance(60_000)
    expect(calls()).toBe(2)
  })

  it('computes the follow-up deadline at completion, not at the trigger', async () => {
    // A request out from t=0 to t=40 s with a hook at t=10 s: the follow-up is
    // due at t=70 s (completion + 30 s), not at t=40 s.
    seed()
    const d = deferred()
    vi.mocked(fetchSessionProvenance).mockReturnValue(d.promise)
    trigger()
    expect(calls()).toBe(1)

    await advance(10_000)
    trigger()                       // in flight: pending only, no timer
    expect(calls()).toBe(1)

    await advance(30_000)           // t = 40 s
    d.resolve(notFound())
    await settle()
    expect(calls()).toBe(1)         // NOT run immediately on completion

    await advance(29_999)           // t = 69.999 s
    expect(calls()).toBe(1)
    await advance(1)                // t = 70.000 s
    expect(calls()).toBe(2)
  })

  it('puts a rejected request into the cooldown exactly like a resolved one', async () => {
    seed()
    const d = deferred()
    vi.mocked(fetchSessionProvenance).mockReturnValueOnce(d.promise)
    trigger()
    d.reject(new Error('host down'))
    await settle()

    vi.mocked(fetchSessionProvenance).mockResolvedValue(notFound())
    await advance(5_000)
    trigger()
    expect(calls()).toBe(1)         // a failure does not license an immediate retry

    await advance(24_999)           // t = 29.999 s
    expect(calls()).toBe(1)
    await advance(1)                // t = 30.000 s
    expect(calls()).toBe(2)
  })

  // --- The deferred run re-checks everything ---

  it('issues no deferred request for a pane that gained an agent during the cooldown', async () => {
    seed()
    await primeCooldown()
    await advance(5_000)
    trigger()
    giveAgent('cc', 'sess-1')
    await advance(25_000)           // t = 30 s
    expect(calls()).toBe(1)
  })

  it('issues no deferred request for a pane re-pointed during the cooldown', async () => {
    const tab = seed()
    await primeCooldown()
    await advance(5_000)
    trigger()
    rebind(tab.id, 'other-code')
    await advance(25_000)
    expect(calls()).toBe(1)
  })

  it('issues no deferred request for a pane terminated during the cooldown', async () => {
    const tab = seed()
    await primeCooldown()
    await advance(5_000)
    trigger()
    repane(tab.id, { terminated: 'session-closed' })
    await advance(25_000)
    expect(calls()).toBe(1)
  })

  it('issues no deferred request once the host attach gate has closed', async () => {
    seed()
    await primeCooldown()
    await advance(5_000)
    trigger()
    setGate('h1', false)
    await advance(25_000)
    expect(calls()).toBe(1)
  })

  it('issues no deferred request for a binding the daemon disowned', async () => {
    seed()
    vi.mocked(fetchSessionProvenance).mockResolvedValue(answer({ tmuxInstance: '333:3000' }))
    trigger()
    await settle()
    await advance(5_000)
    trigger()
    await advance(60_000)
    expect(calls()).toBe(1)
  })

  it('lets the next trigger schedule again after an ineligible deferred run', async () => {
    const tab = seed()
    await primeCooldown()
    await advance(5_000)
    trigger()
    rebind(tab.id, 'other-code')
    await advance(25_000)           // t = 30 s — timer fires, no request
    expect(calls()).toBe(1)

    rebind(tab.id, 'abc123')
    trigger()
    expect(calls()).toBe(2)         // the deadline has passed: immediate
  })

  it('leaves no stale timer handle when a pane is re-pointed away and back', async () => {
    // An implementation that forgets to clear the timer handle on a deferred
    // run it decided NOT to make believes a timer is still armed, and every
    // later deferred run is silently swallowed.
    const tab = seed()
    await primeCooldown()           // request 1 done at t=0, deadline t=30 s

    await advance(5_000)
    trigger()                       // arms a timer for t=30 s
    rebind(tab.id, 'other-code')
    await advance(25_000)           // t=30 s: fires, declines, must clear the handle
    expect(calls()).toBe(1)

    rebind(tab.id, 'abc123')
    await advance(1_000)            // t = 31 s
    trigger()
    await settle()
    expect(calls()).toBe(2)         // request 2 done at t=31 s, deadline t=61 s

    await advance(4_000)            // t = 35 s
    trigger()                       // must arm a FRESH timer for t=61 s
    expect(calls()).toBe(2)

    await advance(25_999)           // t = 60.999 s
    expect(calls()).toBe(2)
    await advance(1)                // t = 61 s
    expect(calls()).toBe(3)
  })

  it('cancels every armed timer on reset', async () => {
    seed()
    await primeCooldown()
    await advance(5_000)
    trigger()                       // a timer is now armed for t=30 s

    resetProvenanceProbes()
    await advance(60_000)
    expect(calls()).toBe(1)
  })

  it('stops asking for good once the answer confirms the record', async () => {
    // The termination proof: a confirm clears `unverified`, and a verified
    // agent is not eligible, so no number of later triggers costs a request.
    seed()
    giveAgent('cc', 'sess-1', true)
    vi.mocked(fetchSessionProvenance).mockResolvedValue(answer())
    trigger()
    await settle()
    expect(calls()).toBe(1)

    for (const t of [5_000, 30_000, 30_000, 30_000]) {
      await advance(t)
      trigger()
    }
    await advance(60_000)
    expect(calls()).toBe(1)
  })
})
