// spa/src/lib/rebuild/engine.test.ts — the rebuild operation (spec §4.8).
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { rebuildPane, retryResume, attachAnyway } from './engine'
import type { RebuildDeps } from './engine'
import { useRebuildStore } from '../../stores/useRebuildStore'
import { useHostStore } from '../../stores/useHostStore'
import { useTabStore } from '../../stores/useTabStore'
import { useSessionStore } from '../../stores/useSessionStore'
import type { Session } from '../host-api'
import type { PaneRebuildRecord, Tab } from '../../types/tab'

const plan = { createSession: true, applyCwd: true, runResume: true }

/** A full `Session`, so the dep-injected fakes stay type-checked. */
function session(over: Partial<Session>): Session {
  return {
    code: 'c', name: 'n', cwd: '', mode: 'terminal',
    cc_session_id: '', cc_model: '', has_relay: false, ...over,
  }
}

// Fixtures. Every case seeds its own host + tab + pane + record; the
// absent-host guard is correct and would otherwise fail every happy path.
function seedHost(hostId: string, over: Partial<{ ip: string; port: number; token: string | null }> = {}) {
  useHostStore.setState({
    hosts: { [hostId]: { id: hostId, name: hostId, ip: '127.0.0.1', port: 7860, token: null, order: 0, ...over } },
    hostOrder: [hostId], activeHostId: hostId, runtime: { [hostId]: { status: 'connected', attachReady: true } },
  })
}

function seedPane(hostId: string, tabId: string, paneId: string, record: Partial<PaneRebuildRecord>) {
  const tab: Tab = {
    id: tabId, pinned: false, locked: false, createdAt: 0,
    layout: { type: 'leaf' as const, pane: { id: paneId, content: {
      kind: 'tmux-session' as const, hostId, sessionCode: 'old111', mode: 'terminal' as const,
      cachedName: 'dev', tmuxInstance: '111:1000', terminated: 'tmux-restarted' as const,
      rebuild: { sessionName: 'dev', tmuxInstance: '111:1000', capturedAt: 1, ...record },
    } } },
  }
  useTabStore.setState({ tabs: { [tabId]: tab }, tabOrder: [tabId], activeTabId: tabId })
}

/** Drop a host the way the Hosts page does, mid-operation. */
function removeHostFromStore() {
  useHostStore.setState({ hosts: {}, hostOrder: [], activeHostId: null, runtime: {} })
}

/** Re-point a pane at someone else's session, mid-operation. */
function rebindPane(tabId: string, _paneId: string, sessionCode: string) {
  const tab = useTabStore.getState().tabs[tabId]
  const layout = tab.layout
  if (layout.type !== 'leaf') throw new Error('fixture is a leaf')
  useTabStore.setState({
    tabs: {
      [tabId]: {
        ...tab,
        layout: { ...layout, pane: { ...layout.pane, content: { ...layout.pane.content, sessionCode } as never } },
      },
    },
  })
}

/** A promise the test resolves at the exact moment it wants the race to open. */
function deferred<T = void>() {
  let resolve!: (v: T) => void
  const promise = new Promise<T>((res) => { resolve = res })
  return { promise, resolve }
}

function paneContent(tabId: string, paneId: string) {
  const layout = useTabStore.getState().tabs[tabId].layout
  if (layout.type !== 'leaf' || layout.pane.id !== paneId) throw new Error('fixture is a leaf')
  return layout.pane.content
}

describe('rebuildPane', () => {
  beforeEach(() => {
    useRebuildStore.setState({ operations: {}, lockedBy: null })
    useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
    seedHost('h1')
    seedPane('h1', 't1', 'p1', { cwd: '/w', resumeCommand: 'claude --resume S1',
      agent: { type: 'cc', sessionId: 'S1', updatedAt: 1 } })
    vi.unstubAllGlobals()
  })

  it('retries the name only on 409 and uses the returned name', async () => {
    const create = vi.fn()
      .mockRejectedValueOnce(Object.assign(new Error('conflict'), { status: 409 }))
      .mockResolvedValueOnce(session({ code: 'new1', name: 'dev-2', tmux_instance: '222:2000' }))
    const report = await rebuildPane('h1', 't1', 'p1', plan, { createSession: create, sendKeys: vi.fn() })
    expect(create).toHaveBeenCalledTimes(2)
    expect(create.mock.calls[1][1]).toBe('dev-2')
    expect(report.created).toEqual({ code: 'new1', name: 'dev-2', tmuxInstance: '222:2000' })
  })

  it('does not retry on 400 or 500', async () => {
    for (const status of [400, 500]) {
      const create = vi.fn().mockRejectedValue(Object.assign(new Error('nope'), { status }))
      const report = await rebuildPane('h1', 't1', 'p1', plan, { createSession: create, sendKeys: vi.fn() })
      expect(create).toHaveBeenCalledTimes(1)
      expect(report.steps.create.status).toBe('failed')
    }
  })

  it('sends the resume before re-pointing the pane', async () => {
    const order: string[] = []
    const create = vi.fn(async () => { order.push('create'); return session({ code: 'new1', name: 'dev', tmux_instance: '222:2000' }) })
    const sendKeys = vi.fn(async () => { order.push('resume') })
    const repoint = vi.fn(() => { order.push('repoint') })
    await rebuildPane('h1', 't1', 'p1', plan, { createSession: create, sendKeys, repoint })
    expect(order).toEqual(['create', 'resume', 'repoint'])
  })

  it('keeps the created session in the report and skips re-point when resume fails', async () => {
    const repoint = vi.fn()
    const report = await rebuildPane('h1', 't1', 'p1', plan, {
      createSession: vi.fn(async () => session({ code: 'new1', name: 'dev', tmux_instance: '222:2000' })),
      sendKeys: vi.fn(async () => { throw new Error('send-keys failed: 500') }),
      repoint,
    })
    expect(report.created?.code).toBe('new1')
    expect(report.steps.resume.status).toBe('failed')
    expect(repoint).not.toHaveBeenCalled()
    expect(report.repointed).toBe(false)
    expect(useRebuildStore.getState().operations['p1'].report.created?.code).toBe('new1')
  })

  it('refuses to run against a host that is gone', async () => {
    const create = vi.fn()
    // 'gone' is deliberately not seeded — pinHost must throw before any request.
    const report = await rebuildPane('gone', 't1', 'p1', plan, { createSession: create, sendKeys: vi.fn() })
    expect(create).not.toHaveBeenCalled()
    expect(report.steps.create.status).toBe('failed')
  })

  it('aborts before the resume when the host disappears mid-operation', async () => {
    const sendKeys = vi.fn()
    const report = await rebuildPane('h1', 't1', 'p1', plan, {
      createSession: vi.fn(async () => { removeHostFromStore(); return session({ code: 'new1', name: 'dev', tmux_instance: '222:2000' }) }),
      sendKeys,
    })
    expect(sendKeys).not.toHaveBeenCalled()
    expect(report.created?.code).toBe('new1')
    expect(report.steps.resume.status).toBe('failed')
  })

  it('skips re-point when the pane binding changed mid-flight', async () => {
    const repoint = vi.fn()
    const report = await rebuildPane('h1', 't1', 'p1', plan, {
      createSession: vi.fn(async () => session({ code: 'new1', name: 'dev', tmux_instance: '222:2000' })),
      sendKeys: vi.fn(async () => { rebindPane('t1', 'p1', 'someone-else') }),
      repoint,
    })
    expect(repoint).not.toHaveBeenCalled()
    expect(report.steps.repoint.status).toBe('skipped')
    expect(report.repointed).toBe(false)
  })

  it('re-points the pane onto the new session and clears terminated', async () => {
    const report = await rebuildPane('h1', 't1', 'p1', plan, {
      createSession: vi.fn(async () => session({ code: 'new1', name: 'dev-2', cwd: '/w', tmux_instance: '222:2000' })),
      sendKeys: vi.fn(),
    })
    expect(report.repointed).toBe(true)
    const c = paneContent('t1', 'p1')
    expect(c).toMatchObject({
      kind: 'tmux-session', sessionCode: 'new1', cachedName: 'dev-2', tmuxInstance: '222:2000',
    })
    expect('terminated' in c ? c.terminated : undefined).toBeUndefined()
    expect(c.kind === 'tmux-session' ? c.rebuild?.sessionName : '').toBe('dev-2')
    // Synced into the session store the way restore.ts does.
    expect(useSessionStore.getState().sessions['h1']?.map((s) => s.code)).toEqual(['new1'])
  })

  // The dead code is dropped from the session store so it cannot linger as a
  // ghost — but only when the cached entry really is the dead session. After a
  // tmux restart the code can already belong to a live stranger.
  it('evicts the dead code when the cached entry is that same generation', async () => {
    useSessionStore.setState({ sessions: { h1: [session({ code: 'old111', name: 'dev', tmux_instance: '111:1000' })] } })
    await rebuildPane('h1', 't1', 'p1', plan, {
      createSession: vi.fn(async () => session({ code: 'new1', name: 'dev-2', tmux_instance: '222:2000' })),
      sendKeys: vi.fn(),
    })
    expect(useSessionStore.getState().sessions['h1']?.map((s) => s.code)).toEqual(['new1'])
  })

  it('evicts a cached entry that carries no generation, as before', async () => {
    useSessionStore.setState({ sessions: { h1: [session({ code: 'old111', name: 'dev' })] } })
    seedPane('h1', 't1', 'p1', { cwd: '/w' })
    useTabStore.setState((state) => {
      const tab = state.tabs['t1']
      const layout = tab.layout
      if (layout.type !== 'leaf') throw new Error('fixture is a leaf')
      return { tabs: { t1: { ...tab, layout: { ...layout, pane: { ...layout.pane,
        content: { ...layout.pane.content, tmuxInstance: '' } as never } } } } }
    })
    await rebuildPane('h1', 't1', 'p1', plan, {
      createSession: vi.fn(async () => session({ code: 'new1', name: 'dev-2', tmux_instance: '222:2000' })),
      sendKeys: vi.fn(),
    })
    expect(useSessionStore.getState().sessions['h1']?.map((s) => s.code)).toEqual(['new1'])
  })

  it('keeps a live session that merely reuses the dead code', async () => {
    // tmux restarted; `old111` is now somebody else's live session.
    useSessionStore.setState({ sessions: { h1: [session({ code: 'old111', name: 'not-ours', tmux_instance: '222:2000' })] } })
    await rebuildPane('h1', 't1', 'p1', plan, {
      createSession: vi.fn(async () => session({ code: 'new1', name: 'dev-2', tmux_instance: '222:2000' })),
      sendKeys: vi.fn(),
    })
    expect(useSessionStore.getState().sessions['h1']?.map((s) => s.code)).toEqual(['old111', 'new1'])
  })

  it('passes the recorded cwd only when the plan applies it', async () => {
    const create: NonNullable<RebuildDeps['createSession']> =
      vi.fn(async () => session({ code: 'new1', name: 'dev', tmux_instance: '222:2000' }))
    await rebuildPane('h1', 't1', 'p1', { ...plan, applyCwd: false }, { createSession: create, sendKeys: vi.fn() })
    expect(vi.mocked(create).mock.calls[0][2]).toBe('')
  })

  it('skips the resume when the record has no resume command', async () => {
    seedPane('h1', 't1', 'p1', { cwd: '/w' })
    const sendKeys = vi.fn()
    const report = await rebuildPane('h1', 't1', 'p1', plan, {
      createSession: vi.fn(async () => session({ code: 'new1', name: 'dev', tmux_instance: '222:2000' })),
      sendKeys,
    })
    expect(sendKeys).not.toHaveBeenCalled()
    expect(report.steps.resume.status).toBe('skipped')
    expect(report.repointed).toBe(true)
  })
})

describe('rebuildPane — production transport', () => {
  beforeEach(() => {
    useRebuildStore.setState({ operations: {}, lockedBy: null })
    useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
    vi.unstubAllGlobals()
  })

  it('uses the pinned host and retries only on 409 through the real transport', async () => {
    seedHost('h1', { ip: '10.0.0.9', port: 7860, token: 'tk' })
    seedPane('h1', 't1', 'p1', { sessionName: 'dev', cwd: '/w', resumeCommand: 'claude -c' })
    const calls: string[] = []
    const seen: RequestInit[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit) => {
      calls.push(url)
      seen.push(init)
      if (url.endsWith('/api/sessions') && calls.filter((u) => u.endsWith('/api/sessions')).length === 1) {
        return new Response('session already exists: dev', { status: 409, statusText: 'Conflict' })
      }
      if (url.endsWith('/api/sessions')) {
        return new Response(JSON.stringify({ code: 'new1', name: 'dev-2', tmux_instance: '222:2000' }), { status: 200 })
      }
      return new Response('{}', { status: 200 })
    }))

    const report = await rebuildPane('h1', 't1', 'p1', plan)
    expect(calls.every((u) => u.startsWith('http://10.0.0.9:7860'))).toBe(true)
    expect(calls.filter((u) => u.endsWith('/api/sessions'))).toHaveLength(2)
    expect(calls.some((u) => u.endsWith('/api/sessions/new1/send-keys'))).toBe(true)
    expect(report.created?.name).toBe('dev-2')
    // Exact header name/format copied from useHostStore.getAuthHeaders.
    expect((seen[0].headers as Record<string, string>).Authorization).toBe('Bearer tk')
  })

  it('stops at the retry cap', async () => {
    seedHost('h1'); seedPane('h1', 't1', 'p1', { sessionName: 'dev' })
    vi.stubGlobal('fetch', vi.fn(async () => new Response('dup', { status: 409, statusText: 'Conflict' })))
    const report = await rebuildPane('h1', 't1', 'p1', plan)
    expect(report.steps.create.status).toBe('failed')
    expect((globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls).toHaveLength(5)
  })

  it('never contacts the active host when the target host is gone', async () => {
    // 'other' is the ACTIVE host; the rebuild targets 'h1', which is absent.
    useHostStore.setState({
      hosts: { other: { id: 'other', name: 'other', ip: '10.0.0.1', port: 7860, token: null, order: 0 } },
      hostOrder: ['other'], activeHostId: 'other', runtime: {},
    })
    seedPane('h1', 't1', 'p1', { sessionName: 'dev', resumeCommand: 'claude -c' })
    const fetchMock = vi.fn(async () => new Response('{}', { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)

    const report = await rebuildPane('h1', 't1', 'p1', plan)
    expect(fetchMock).not.toHaveBeenCalled()
    expect(report.steps.create.status).toBe('failed')
    expect(report.repointed).toBe(false)
  })

  // --- send-keys generation precondition (spec §4.6.2) ---
  //
  // The local session cache cannot prove what a code points at, so the
  // expectation travels with the request and the daemon decides.

  /** The parsed body of the one send-keys request the operation made. */
  function sendKeysBody(fetchMock: ReturnType<typeof vi.fn>): Record<string, unknown> {
    const call = fetchMock.mock.calls.find(([url]) => String(url).endsWith('/send-keys'))
    if (!call) throw new Error('no send-keys request was made')
    return JSON.parse(String((call[1] as RequestInit).body))
  }

  it('states the created session generation on the resume', async () => {
    seedHost('h1')
    seedPane('h1', 't1', 'p1', { sessionName: 'dev', cwd: '/w', resumeCommand: 'claude -c' })
    const fetchMock = vi.fn(async (url: string) => {
      if (url.endsWith('/api/sessions')) {
        return new Response(JSON.stringify({ code: 'new1', name: 'dev', tmux_instance: '222:2000' }), { status: 200 })
      }
      return new Response(null, { status: 204 })
    })
    vi.stubGlobal('fetch', fetchMock)

    const report = await rebuildPane('h1', 't1', 'p1', plan)
    expect(report.steps.resume.status).toBe('ok')
    expect(sendKeysBody(fetchMock)).toEqual({
      keys: 'claude -c\n', expected_tmux_instance: '222:2000',
    })
  })

  it('reports a 409 from the generation precondition as a refusal, and does not re-point', async () => {
    seedHost('h1')
    seedPane('h1', 't1', 'p1', { sessionName: 'dev', cwd: '/w', resumeCommand: 'claude -c' })
    const fetchMock = vi.fn(async (url: string) => {
      if (url.endsWith('/api/sessions')) {
        return new Response(JSON.stringify({ code: 'new1', name: 'dev', tmux_instance: '222:2000' }), { status: 200 })
      }
      return new Response('session new1 belongs to another tmux generation', { status: 409, statusText: 'Conflict' })
    })
    vi.stubGlobal('fetch', fetchMock)

    const report = await rebuildPane('h1', 't1', 'p1', plan)
    expect(report.steps.resume.status).toBe('failed')
    expect(report.steps.resume.error).toMatch(/generation/i)
    // The refusal is final, not a transient failure to grind against.
    expect(fetchMock.mock.calls.filter(([u]) => String(u).endsWith('/send-keys'))).toHaveLength(1)
    expect(report.repointed).toBe(false)
    expect(paneContent('t1', 'p1')).toMatchObject({ sessionCode: 'old111' })
  })

  it('states the pane binding generation when the resume goes to the pane own session', async () => {
    seedHost('h1')
    seedPane('h1', 't1', 'p1', { sessionName: 'dev', resumeCommand: 'claude -c' })
    const fetchMock = vi.fn(async () => new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)

    // `seedPane` binds the pane to old111 @ 111:1000.
    await rebuildPane('h1', 't1', 'p1', { createSession: false, applyCwd: false, runResume: true })
    expect(sendKeysBody(fetchMock)).toEqual({
      keys: 'claude -c\n', expected_tmux_instance: '111:1000',
    })
  })

  it('refuses the resume when the pane binding states no generation', async () => {
    seedHost('h1')
    seedPane('h1', 't1', 'p1', { sessionName: 'dev', resumeCommand: 'claude -c' })
    // A legacy pane that never learnt its generation can assert nothing, so a
    // rebuild resume has no authority to send at all (spec §4.6.2). Sending
    // without an expectation is Quick Commands' behaviour, not a rebuild's.
    const tab = useTabStore.getState().tabs.t1
    const layout = tab.layout
    if (layout.type !== 'leaf') throw new Error('fixture is a leaf')
    useTabStore.setState({ tabs: { t1: { ...tab, layout: { ...layout, pane: { ...layout.pane,
      content: { ...layout.pane.content, tmuxInstance: '' } as never } } } } })

    const fetchMock = vi.fn(async (_url: string) => new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)

    const report = await rebuildPane('h1', 't1', 'p1', { createSession: false, applyCwd: false, runResume: true })
    expect(fetchMock.mock.calls.filter(([u]) => String(u).endsWith('/send-keys'))).toHaveLength(0)
    expect(report.steps.resume.status).toBe('failed')
    expect(report.steps.resume.error).toMatch(/generation/i)
    expect(report.repointed).toBe(false)
  })

  it('refuses the resume when the create response carried no generation, keeping the session', async () => {
    seedHost('h1')
    seedPane('h1', 't1', 'p1', { sessionName: 'dev', cwd: '/w', resumeCommand: 'claude -c' })
    // The daemon's own instance probe failed or timed out. An unknown
    // generation authorises nothing — but the session it just made is real.
    const fetchMock = vi.fn(async (url: string) => {
      if (url.endsWith('/api/sessions')) {
        return new Response(JSON.stringify({ code: 'new1', name: 'dev', tmux_instance: '' }), { status: 200 })
      }
      return new Response(null, { status: 204 })
    })
    vi.stubGlobal('fetch', fetchMock)

    const report = await rebuildPane('h1', 't1', 'p1', plan)
    expect(fetchMock.mock.calls.filter(([u]) => String(u).endsWith('/send-keys'))).toHaveLength(0)
    expect(report.created).toEqual({ code: 'new1', name: 'dev', tmuxInstance: '' })
    expect(report.steps.create.status).toBe('ok')
    expect(report.steps.resume.status).toBe('failed')
    expect(report.steps.resume.error).toMatch(/generation/i)
    expect(report.repointed).toBe(false)
    expect(paneContent('t1', 'p1')).toMatchObject({ sessionCode: 'old111' })
  })
})

describe('retryResume / attachAnyway', () => {
  beforeEach(() => {
    useRebuildStore.setState({ operations: {}, lockedBy: null })
    useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
    seedHost('h1')
    seedPane('h1', 't1', 'p1', { cwd: '/w', resumeCommand: 'claude --resume S1' })
    vi.unstubAllGlobals()
  })

  async function failedResumeOperation() {
    return rebuildPane('h1', 't1', 'p1', plan, {
      createSession: vi.fn(async () => session({ code: 'new1', name: 'dev-2', tmux_instance: '222:2000' })),
      sendKeys: vi.fn(async () => { throw new Error('boom') }),
    })
  }

  it('retryResume re-sends against the already-created session, then re-points', async () => {
    await failedResumeOperation()
    const sendKeys = vi.fn()
    const report = await retryResume('p1', { sendKeys })
    expect(sendKeys).toHaveBeenCalledWith('h1', 'new1', 'claude --resume S1', '222:2000')
    expect(report.steps.create.status).toBe('ok')
    expect(report.steps.resume.status).toBe('ok')
    expect(report.repointed).toBe(true)
    expect(paneContent('t1', 'p1')).toMatchObject({ sessionCode: 'new1' })
  })

  it('attachAnyway re-points without re-sending the resume', async () => {
    await failedResumeOperation()
    const sendKeys = vi.fn()
    const report = await attachAnyway('p1', { sendKeys })
    expect(sendKeys).not.toHaveBeenCalled()
    expect(report.steps.resume.status).toBe('skipped')
    expect(report.repointed).toBe(true)
    expect(paneContent('t1', 'p1')).toMatchObject({ sessionCode: 'new1' })
  })

  it('reports a failure when no operation is recorded for the pane', async () => {
    const report = await retryResume('nope')
    expect(report.steps.resume.status).toBe('failed')
  })
})

// ---------------------------------------------------------------------------
// The shared operation lock (spec §4.11). The owner is `rebuild:<paneId>`, so
// re-entrancy alone would let two operations on ONE pane both proceed — the
// engine therefore checks `operations[paneId].status` BEFORE it acquires.
// ---------------------------------------------------------------------------
describe('rebuildPane — operation lock', () => {
  beforeEach(() => {
    useRebuildStore.setState({ operations: {}, lockedBy: null })
    useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
    seedHost('h1')
    seedPane('h1', 't1', 'p1', { cwd: '/w', resumeCommand: 'claude --resume S1' })
    vi.unstubAllGlobals()
  })

  it('holds the lock for the duration and releases it afterwards', async () => {
    let heldDuringCreate: string | null = null
    await rebuildPane('h1', 't1', 'p1', plan, {
      createSession: vi.fn(async () => {
        heldDuringCreate = useRebuildStore.getState().lockedBy
        return session({ code: 'new1', name: 'dev', tmux_instance: '222:2000' })
      }),
      sendKeys: vi.fn(),
    })
    expect(heldDuringCreate).toBe('rebuild:p1')
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })

  it('releases the lock even when the operation fails before it starts', async () => {
    const report = await rebuildPane('gone', 't1', 'p1', plan, { createSession: vi.fn(), sendKeys: vi.fn() })
    expect(report.steps.create.status).toBe('failed')
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })

  it('refuses a second concurrent operation on the same pane', async () => {
    const neverResolves = vi.fn(() => new Promise<Session>(() => {}))
    const first = rebuildPane('h1', 't1', 'p1', plan, { createSession: neverResolves, sendKeys: vi.fn() })
    const blocked = vi.fn()
    const second = await rebuildPane('h1', 't1', 'p1', plan, { createSession: blocked, sendKeys: vi.fn() })
    expect(second.steps.create.status).toBe('failed')
    expect(blocked).not.toHaveBeenCalled()
    // The refusal must neither unlock nor overwrite the running operation.
    expect(useRebuildStore.getState().lockedBy).toBe('rebuild:p1')
    expect(useRebuildStore.getState().operations['p1'].status).toBe('running')
    void first
  })

  it('refuses to start while a legacy snapshot action holds the lock', async () => {
    useRebuildStore.getState().acquireOperationLock('snapshot:restoreAll')
    const create = vi.fn()
    const report = await rebuildPane('h1', 't1', 'p1', plan, { createSession: create, sendKeys: vi.fn() })
    expect(create).not.toHaveBeenCalled()
    expect(report.steps.create.status).toBe('failed')
    expect(report.steps.create.error).toContain('snapshot:restoreAll')
    expect(useRebuildStore.getState().lockedBy).toBe('snapshot:restoreAll')
  })

  it('refuses retryResume while a legacy snapshot action holds the lock', async () => {
    await rebuildPane('h1', 't1', 'p1', plan, {
      createSession: vi.fn(async () => session({ code: 'new1', name: 'dev', tmux_instance: '222:2000' })),
      sendKeys: vi.fn(async () => { throw new Error('boom') }),
    })
    useRebuildStore.getState().acquireOperationLock('snapshot:restoreAll')
    const sendKeys = vi.fn()
    const report = await retryResume('p1', { sendKeys })
    expect(sendKeys).not.toHaveBeenCalled()
    expect(report.steps.resume.status).toBe('failed')
    expect(useRebuildStore.getState().lockedBy).toBe('snapshot:restoreAll')
  })

  it('attachAnyway takes and releases the lock', async () => {
    await rebuildPane('h1', 't1', 'p1', plan, {
      createSession: vi.fn(async () => session({ code: 'new1', name: 'dev', tmux_instance: '222:2000' })),
      sendKeys: vi.fn(async () => { throw new Error('boom') }),
    })
    const report = await attachAnyway('p1', { sendKeys: vi.fn() })
    expect(report.repointed).toBe(true)
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })
})

// ---------------------------------------------------------------------------
// A retry runs against a session that was created some time ago. Before it
// sends anything it has to prove the target is still the same machine and
// still the same session — the host's address may have been edited, and tmux
// may have restarted and handed the code to somebody else.
// ---------------------------------------------------------------------------
describe('retryResume / attachAnyway — target identity', () => {
  beforeEach(() => {
    useRebuildStore.setState({ operations: {}, lockedBy: null })
    useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
    seedHost('h1', { ip: '10.0.0.9' })
    seedPane('h1', 't1', 'p1', { cwd: '/w', resumeCommand: 'claude --resume S1' })
    vi.unstubAllGlobals()
  })

  async function failedResumeOperation() {
    return rebuildPane('h1', 't1', 'p1', plan, {
      createSession: vi.fn(async () => session({ code: 'new1', name: 'dev-2', tmux_instance: '222:2000' })),
      sendKeys: vi.fn(async () => { throw new Error('boom') }),
    })
  }

  /** What the SPA currently believes lives on the host. */
  function seedSessions(...sessions: Session[]) {
    useSessionStore.setState({ sessions: { h1: sessions } })
  }

  it('refuses the retry when the host address changed since the operation started', async () => {
    await failedResumeOperation()
    // The user edited the host: same id, different machine.
    seedHost('h1', { ip: '10.0.0.77' })
    const sendKeys = vi.fn()
    const report = await retryResume('p1', { sendKeys })
    expect(sendKeys).not.toHaveBeenCalled()
    expect(report.steps.resume.status).toBe('failed')
    expect(report.repointed).toBe(false)
    expect(paneContent('t1', 'p1')).toMatchObject({ sessionCode: 'old111' })
  })

  it('refuses the retry when the code now belongs to a different tmux generation', async () => {
    await failedResumeOperation()
    // tmux restarted and handed `new1` to somebody else.
    seedSessions(session({ code: 'new1', name: 'not-ours', tmux_instance: '333:3000' }))
    const sendKeys = vi.fn()
    const report = await retryResume('p1', { sendKeys })
    expect(sendKeys).not.toHaveBeenCalled()
    expect(report.steps.resume.status).toBe('failed')
    expect(report.steps.resume.error).toMatch(/generation|instance/i)
    expect(report.repointed).toBe(false)
  })

  it('refuses to attach anyway onto a code that changed generation', async () => {
    await failedResumeOperation()
    seedSessions(session({ code: 'new1', name: 'not-ours', tmux_instance: '333:3000' }))
    const repoint = vi.fn()
    const report = await attachAnyway('p1', { repoint })
    expect(repoint).not.toHaveBeenCalled()
    expect(report.repointed).toBe(false)
    expect(paneContent('t1', 'p1')).toMatchObject({ sessionCode: 'old111' })
  })

  it('retries when the session store still shows the session it created', async () => {
    await failedResumeOperation()
    seedSessions(session({ code: 'new1', name: 'dev-2', tmux_instance: '222:2000' }))
    const sendKeys = vi.fn()
    const report = await retryResume('p1', { sendKeys })
    expect(sendKeys).toHaveBeenCalledWith('h1', 'new1', 'claude --resume S1', '222:2000')
    expect(report.repointed).toBe(true)
  })

  it('retries when the SPA has no session list to check against', async () => {
    await failedResumeOperation()
    const sendKeys = vi.fn()
    const report = await retryResume('p1', { sendKeys })
    expect(sendKeys).toHaveBeenCalledWith('h1', 'new1', 'claude --resume S1', '222:2000')
    expect(report.repointed).toBe(true)
  })
})

// ---------------------------------------------------------------------------
// A refusal is a result. `rebuildPane` can stop before `beginOperation` ever
// runs, and the store is where the panel looks — so those reports have to land
// there too, without destroying anything worth more.
// ---------------------------------------------------------------------------
describe('rebuildPane — refusals reach the store', () => {
  beforeEach(() => {
    useRebuildStore.setState({ operations: {}, lockedBy: null })
    useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
    seedHost('h1')
    seedPane('h1', 't1', 'p1', { cwd: '/w', resumeCommand: 'claude --resume S1' })
    vi.unstubAllGlobals()
  })

  it('publishes an unknown-host refusal under the pane binding', async () => {
    await rebuildPane('gone', 't1', 'p1', plan, { createSession: vi.fn(), sendKeys: vi.fn() })
    const op = useRebuildStore.getState().operations['p1']
    expect(op?.status).toBe('done')
    expect(op?.report.steps.create.status).toBe('failed')
    expect(op?.report.steps.create.error).toContain('not configured')
    // Under the pane's own binding, so the panel actually shows it.
    expect(op?.binding).toEqual({ hostId: 'h1', sessionCode: 'old111', tmuxInstance: '111:1000' })
  })

  it('publishes a lock refusal', async () => {
    useRebuildStore.getState().acquireOperationLock('snapshot:restoreAll')
    await rebuildPane('h1', 't1', 'p1', plan, { createSession: vi.fn(), sendKeys: vi.fn() })
    expect(useRebuildStore.getState().operations['p1']?.report.steps.create.error)
      .toContain('snapshot:restoreAll')
  })

  it('never overwrites an operation that created a session', async () => {
    await rebuildPane('h1', 't1', 'p1', plan, {
      createSession: vi.fn(async () => session({ code: 'new1', name: 'dev', tmux_instance: '222:2000' })),
      sendKeys: vi.fn(async () => { throw new Error('boom') }),
    })
    useRebuildStore.getState().acquireOperationLock('snapshot:restoreAll')
    await rebuildPane('h1', 't1', 'p1', plan, { createSession: vi.fn(), sendKeys: vi.fn() })
    const op = useRebuildStore.getState().operations['p1']
    expect(op?.createdSession?.code).toBe('new1')
    expect(op?.report.created?.code).toBe('new1')
  })

  it('tells the panel why a retry was refused, keeping the created session', async () => {
    await rebuildPane('h1', 't1', 'p1', plan, {
      createSession: vi.fn(async () => session({ code: 'new1', name: 'dev', tmux_instance: '222:2000' })),
      sendKeys: vi.fn(async () => { throw new Error('boom') }),
    })
    useRebuildStore.getState().acquireOperationLock('snapshot:restoreAll')
    await retryResume('p1', { sendKeys: vi.fn() })
    const op = useRebuildStore.getState().operations['p1']
    expect(op?.report.steps.resume.error).toContain('snapshot:restoreAll')
    expect(op?.createdSession?.code).toBe('new1')
  })
})

// ---------------------------------------------------------------------------
// A re-point writes the operation's session code onto the pane, and the pane's
// `hostId` is resolved fresh every time the terminal builds its URL. So the
// re-point has to re-assert the PINNED host, not just the binding: if the
// host's address was edited while the create or the send-keys was in flight,
// the code belongs to the old machine while the pane now resolves to the new
// one (spec §4.8, "pinned transport").
// ---------------------------------------------------------------------------
describe('re-point — the pinned host', () => {
  beforeEach(() => {
    useRebuildStore.setState({ operations: {}, lockedBy: null })
    useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
    seedHost('h1')
    seedPane('h1', 't1', 'p1', { cwd: '/w', resumeCommand: 'claude --resume S1' })
    vi.unstubAllGlobals()
  })

  it('refuses the re-point when the host address is edited while the resume is in flight', async () => {
    const sending = deferred()
    const finishSend = deferred()
    const run = rebuildPane('h1', 't1', 'p1', plan, {
      createSession: vi.fn(async () => session({ code: 'new1', name: 'dev', tmux_instance: '222:2000' })),
      sendKeys: vi.fn(async () => { sending.resolve(); await finishSend.promise }),
    })
    await sending.promise
    // The user edits the host's address in Settings → Hosts, mid-operation.
    seedHost('h1', { ip: '10.0.0.9' })
    finishSend.resolve()
    const report = await run

    expect(report.steps.resume.status).toBe('ok')
    expect(report.steps.repoint.status).toBe('failed')
    expect(report.steps.repoint.error).toMatch(/host h1 changed/)
    expect(report.repointed).toBe(false)
    // The created session is not lost, and nothing was written anywhere.
    expect(report.created?.code).toBe('new1')
    expect(paneContent('t1', 'p1')).toMatchObject({ sessionCode: 'old111', terminated: 'tmux-restarted' })
    expect(useSessionStore.getState().sessions['h1']).toBeUndefined()
  })

  it('refuses the re-point when the host is removed while the resume is in flight', async () => {
    const sending = deferred()
    const finishSend = deferred()
    const run = rebuildPane('h1', 't1', 'p1', plan, {
      createSession: vi.fn(async () => session({ code: 'new1', name: 'dev', tmux_instance: '222:2000' })),
      sendKeys: vi.fn(async () => { sending.resolve(); await finishSend.promise }),
    })
    await sending.promise
    removeHostFromStore()
    finishSend.resolve()
    const report = await run

    expect(report.steps.repoint.status).toBe('failed')
    expect(report.repointed).toBe(false)
    expect(paneContent('t1', 'p1')).toMatchObject({ sessionCode: 'old111' })
  })

  it('refuses attachAnyway\'s re-point when the host changed after the create', async () => {
    await rebuildPane('h1', 't1', 'p1', plan, {
      createSession: vi.fn(async () => session({ code: 'new1', name: 'dev', tmux_instance: '222:2000' })),
      sendKeys: vi.fn(async () => { throw new Error('boom') }),
    })
    expect(paneContent('t1', 'p1')).toMatchObject({ sessionCode: 'old111' })
    seedHost('h1', { ip: '10.0.0.9' })
    const report = await attachAnyway('p1', { sendKeys: vi.fn() })
    expect(report.repointed).toBe(false)
    expect(paneContent('t1', 'p1')).toMatchObject({ sessionCode: 'old111' })
  })
})
