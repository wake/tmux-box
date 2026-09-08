// spa/src/hooks/useMultiHostEventWs.provenance-probe.test.ts — two of the three
// provenance triggers (spec §5.4): the reconciled `sessions` sweep and the hook
// broadcast. The third (pane attach) is tested in `SessionPaneContent.test.tsx`.
//
// Unlike the cwd-probe trigger test, this file does NOT stub the probe module.
// A stub would make the gate, the eligibility rule and the shared binding key
// unobservable, and those are exactly the ways a call site can be wrong while
// the scheduler itself is green. `fetchSessionProvenance` is the seam instead:
// "a request was scheduled" means the daemon was asked.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useTabStore } from '../stores/useTabStore'
import { useAgentStore } from '../stores/useAgentStore'
import { createTab, type TmuxSessionContent, type Tab } from '../types/tab'
import { resetProvenanceProbes } from '../lib/rebuild/provenance-probe'
import { fetchSessionProvenance, type SessionProvenance } from '../lib/host-api'
import { openAttachGate } from '../lib/rebuild/attach-gate'

vi.mock('../lib/host-connection', () => ({
  checkHealth: vi.fn(async () => ({ daemon: 'connected', latency: 3, ticket: 'tk' })),
}))
// The cwd probe is a different question with its own trigger tests; silence it
// so a stray HTTP call cannot be mistaken for a provenance request.
vi.mock('../lib/rebuild/cwd-probe', () => ({
  probeMissingCwds: vi.fn(),
  probeSessionCwd: vi.fn(),
}))
vi.mock('../lib/host-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/host-api')>()),
  fetchSessionProvenance: vi.fn(),
}))

const { useMultiHostEventWs } = await import('./useMultiHostEventWs')

const HOST = 'h1'
const GEN = '222:2000'

class FakeSocket {
  static OPEN = 1
  readyState = 0
  binaryType = ''
  url: string
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onmessage: ((e: { data: unknown }) => void) | null = null
  onerror: (() => void) | null = null
  send = vi.fn()
  close = vi.fn(() => { this.readyState = 3 })
  constructor(url: string) { this.url = url; sockets.push(this) }
  emit(data: string) { this.onmessage?.({ data }) }
}

let sockets: FakeSocket[] = []

/** The daemon's ownership answer, stamped with the generation it was read in. */
function answer(over?: Partial<SessionProvenance>): SessionProvenance {
  return {
    found: true,
    agentType: 'cc',
    sessionId: 'sess-1',
    cwd: '/w/proj',
    tmuxPaneId: '%12',
    tmuxInstance: GEN,
    lastSeenAt: 1788800000000,
    ...over,
  }
}

/** Put `contents` into the tab store, one single-pane tab each. */
function seed(...contents: Partial<TmuxSessionContent>[]): Tab[] {
  const tabs: Record<string, Tab> = {}
  const made: Tab[] = []
  for (const over of contents) {
    const tab = createTab({
      kind: 'tmux-session', hostId: HOST, sessionCode: 'abc123',
      mode: 'terminal', cachedName: 'dev', tmuxInstance: GEN,
      ...over,
    })
    tabs[tab.id] = tab
    made.push(tab)
  }
  useTabStore.setState({
    tabs, tabOrder: made.map((t) => t.id), activeTabId: made[0]?.id ?? null,
  })
  return made
}

function recordOf(tabId: string) {
  const l = useTabStore.getState().tabs[tabId].layout
  return l.type === 'leaf' && l.pane.content.kind === 'tmux-session' ? l.pane.content.rebuild : undefined
}

const sessionsPayload = (codes: string[], tmuxInstance = GEN) => JSON.stringify({
  type: 'sessions', session: '',
  value: JSON.stringify(codes.map((code) => ({ code, name: code, tmux_instance: tmuxInstance }))),
})

/** A hook broadcast, with whatever `detail` the caller wants normalized. */
const hookPayload = (session: string, detail?: Record<string, unknown>) => JSON.stringify({
  type: 'hook', session,
  value: JSON.stringify({
    agent_type: 'cc', status: 'running', raw_event_name: 'Stop',
    broadcast_ts: Date.now(), ...(detail ? { detail } : {}),
  }),
})

async function mountHook() {
  const view = renderHook(() => useMultiHostEventWs())
  await waitFor(() => expect(sockets).toHaveLength(1))
  return view
}

beforeEach(() => {
  sockets = []
  resetProvenanceProbes()
  vi.mocked(fetchSessionProvenance).mockReset()
  vi.mocked(fetchSessionProvenance).mockResolvedValue(answer({ found: false, agentType: '' }))
  vi.stubGlobal('WebSocket', FakeSocket)
  useHostStore.setState({
    hosts: { [HOST]: { id: HOST, name: 'Host', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST],
    runtime: {},
    activeHostId: HOST,
  })
  useSessionStore.setState({
    fetchHost: vi.fn(async () => {}),
    replaceHost: vi.fn(),
  } as never)
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
})

afterEach(() => {
  resetProvenanceProbes()
  useAgentStore.setState({ statuses: {}, agentTypes: {}, lastEvents: {} } as never)
  vi.unstubAllGlobals()
  useHostStore.getState().reset()
})

describe('useMultiHostEventWs provenance sweep', () => {
  it('asks for every eligible binding on the host once the payload is reconciled', async () => {
    seed(
      { sessionCode: 'aaa' },                                    // eligible
      { sessionCode: 'bbb', rebuild: {                           // already owned
        sessionName: 'dev', tmuxInstance: GEN, capturedAt: 1,
        agent: { type: 'cc', sessionId: 's', updatedAt: 1 },
      } },
      { sessionCode: 'ccc', mode: 'stream' },                    // out of scope
      { sessionCode: 'ddd', terminated: 'session-closed' },      // dead
      { sessionCode: 'eee', hostId: 'h2' },                      // another host
    )
    const view = await mountHook()
    expect(fetchSessionProvenance).not.toHaveBeenCalled()

    act(() => { sockets[0].emit(sessionsPayload(['aaa', 'bbb', 'ccc'])) })

    expect(fetchSessionProvenance).toHaveBeenCalledTimes(1)
    expect(fetchSessionProvenance).toHaveBeenCalledWith(HOST, 'aaa')
    view.unmount()
  })

  it('does not sweep on a payload that could not be parsed', async () => {
    seed({ sessionCode: 'aaa' })
    const view = await mountHook()

    act(() => {
      sockets[0].emit(JSON.stringify({ type: 'sessions', session: '', value: 'not-json' }))
    })

    expect(fetchSessionProvenance).not.toHaveBeenCalled()
    view.unmount()
  })
})

describe('useMultiHostEventWs provenance on a hook broadcast', () => {
  it('asks for a session whose pane still wants provenance', async () => {
    const [tab] = seed({ sessionCode: 'abc123' })
    const view = await mountHook()
    act(() => { openAttachGate(HOST) })
    vi.mocked(fetchSessionProvenance).mockResolvedValue(answer())

    act(() => { sockets[0].emit(hookPayload('abc123')) })

    expect(fetchSessionProvenance).toHaveBeenCalledTimes(1)
    expect(fetchSessionProvenance).toHaveBeenCalledWith(HOST, 'abc123')
    // The generation is not one of the request's arguments, so the only proof
    // that the PANE'S RECORDED one was passed is that the answer, stamped with
    // that same generation, authorises the write. Had the trigger read '' off
    // the event instead, the answer would not have matched what was asked with
    // and nothing would land.
    await waitFor(() => expect(recordOf(tab.id)?.agent?.type).toBe('cc'))
    view.unmount()
  })

  it('shares one binding key with the sweep, so a hook during a request adds none', async () => {
    seed({ sessionCode: 'abc123' })
    const view = await mountHook()
    vi.mocked(fetchSessionProvenance).mockReturnValue(new Promise(() => {}))

    act(() => { sockets[0].emit(sessionsPayload(['abc123'])) })
    expect(fetchSessionProvenance).toHaveBeenCalledTimes(1)

    act(() => { sockets[0].emit(hookPayload('abc123')) })

    // A trigger passing a generation the pane does not hold would key a
    // different binding and slip past the in-flight guard.
    expect(fetchSessionProvenance).toHaveBeenCalledTimes(1)
    view.unmount()
  })

  it('asks nothing for a pane whose agent is already confirmed', async () => {
    seed({
      sessionCode: 'abc123',
      rebuild: {
        sessionName: 'dev', tmuxInstance: GEN, capturedAt: 1,
        agent: { type: 'cc', sessionId: 's', updatedAt: 1 },
      },
    })
    const view = await mountHook()
    act(() => { openAttachGate(HOST) })

    act(() => { sockets[0].emit(hookPayload('abc123')) })

    expect(fetchSessionProvenance).not.toHaveBeenCalled()
    view.unmount()
  })

  it('asks nothing before the host attach gate has opened', async () => {
    seed({ sessionCode: 'abc123' })
    const view = await mountHook()

    act(() => { sockets[0].emit(hookPayload('abc123')) })

    expect(fetchSessionProvenance).not.toHaveBeenCalled()
    view.unmount()
  })

  // THE ORDERING TEST. "Already verified schedules nothing" cannot stand in for
  // this one: that fixture was verified before the hook arrived, so scheduling
  // BEFORE normalization passes it too. Only an event that CAUSES the
  // transition separates the two orderings — hence a pane with no agent, a real
  // `SessionStart` envelope down the real socket path, and no stub on
  // `handleNormalizedEvent`.
  it('schedules after normalization, so a broadcast that fills the record costs no request', async () => {
    const [tab] = seed({ sessionCode: 'abc123' })
    const view = await mountHook()
    act(() => { openAttachGate(HOST) })
    expect(recordOf(tab.id)?.agent).toBeUndefined()

    act(() => {
      sockets[0].emit(hookPayload('abc123', {
        pdx_provenance: {
          owner_session_start: true,
          agent_type: 'cc',
          session_id: 'sess-9',
          cwd: '/w/proj',
          tmux_pane_id: '%12',
          tmux_instance: GEN,
        },
      }))
    })

    expect(recordOf(tab.id)?.agent?.type).toBe('cc')
    expect(recordOf(tab.id)?.agent?.sessionId).toBe('sess-9')
    expect(recordOf(tab.id)?.unverified).toBeFalsy()
    expect(fetchSessionProvenance).not.toHaveBeenCalled()
    view.unmount()
  })
})
