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
    expect(sendKeys).toHaveBeenCalledWith('h1', 'new1', 'claude --resume S1')
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
