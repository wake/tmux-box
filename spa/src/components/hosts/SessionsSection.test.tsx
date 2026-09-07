// spa/src/components/hosts/SessionsSection.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup, act } from '@testing-library/react'
import { SessionsSection } from './SessionsSection'
import { useSessionStore } from '../../stores/useSessionStore'
import { useHostStore } from '../../stores/useHostStore'
import { useAgentStore } from '../../stores/useAgentStore'
import { useQuickCommandStore } from '../../stores/useQuickCommandStore'
import { useModuleEnabledStore } from '../../stores/useModuleEnabledStore'
import { compositeKey } from '../../lib/composite-key'
import { clearModuleRegistry, registerModule } from '../../lib/module-registry'
import { QUICK_COMMAND_SLOTS } from '../../lib/quick-command-slots'
import { createSession } from '../../lib/host-api'
import { executeCommand } from '../../lib/execute-command'

const mockOpenSingletonTab = vi.fn(() => 'tab-1')
const mockSetActiveTab = vi.fn()
const mockInsertTab = vi.fn()
const mockFindWorkspaceByTab = vi.fn<(tabId: string) => unknown>(() => null)
// Mutable so the R1 race test can flip the active tab mid-flight (e.g.
// during await createSession) and assert the executor uses the click-time
// snapshot, not the post-await value.
let mockActiveTabId: string | null = 'tab-host'

vi.mock('../../stores/useTabStore', () => ({
  useTabStore: {
    getState: () => ({
      openSingletonTab: mockOpenSingletonTab,
      setActiveTab: mockSetActiveTab,
      get activeTabId() { return mockActiveTabId },
    }),
  },
}))

vi.mock('../../stores/useWorkspaceStore', () => {
  const store = Object.assign(
    (selector: (s: Record<string, unknown>) => unknown) =>
      selector({ workspaces: [], insertTab: mockInsertTab, findWorkspaceByTab: mockFindWorkspaceByTab }),
    {
      getState: () => ({
        insertTab: mockInsertTab,
        workspaces: [],
        findWorkspaceByTab: mockFindWorkspaceByTab,
      }),
    },
  )
  return { useWorkspaceStore: store }
})

vi.mock('../../lib/host-api', () => ({
  hostFetch: vi.fn().mockResolvedValue({ ok: true, json: () => Promise.resolve({}) }),
  renameSession: vi.fn().mockResolvedValue({ ok: true }),
  createSession: vi.fn(),
}))

vi.mock('../../lib/execute-command', () => ({
  executeCommand: vi.fn().mockResolvedValue(undefined),
}))

const HOST_ID = 'test-host'
const SESSIONS = [
  { code: 'abc', name: 'dev', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false },
]

beforeEach(() => {
  cleanup()
  mockOpenSingletonTab.mockClear()
  mockSetActiveTab.mockClear()
  mockInsertTab.mockClear()
  mockFindWorkspaceByTab.mockReset()
  mockFindWorkspaceByTab.mockReturnValue(null)
  mockActiveTabId = 'tab-host'
  vi.mocked(createSession).mockReset()
  vi.mocked(executeCommand).mockReset()
  vi.mocked(executeCommand).mockResolvedValue(undefined)
  useSessionStore.setState({ sessions: { [HOST_ID]: SESSIONS } })
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST_ID],
    runtime: { [HOST_ID]: { status: 'connected' } },
    activeHostId: HOST_ID,
  })
  useAgentStore.setState({ statuses: {} })
  // Reset Quick Commands store with explicit fields (feedback_zustand_harness_setstate.md)
  useQuickCommandStore.setState({ global: [], byHost: {}, bindings: {} })
  // Module registry — quick-commands needs to be a known disableable module so
  // <CommandSlot> / v1 <QuickCommandMenu> module-enabled gates pass.
  clearModuleRegistry()
  registerModule({ id: 'quick-commands', name: 'Quick Commands', disableable: true })
  // Reset module enabled overrides
  useModuleEnabledStore.setState({ enabled: {}, baseline: null })
})

describe('SessionsSection', () => {
  it('shows "No sessions" when sessions list is empty', () => {
    useSessionStore.setState({ sessions: { [HOST_ID]: [] } })
    render(<SessionsSection hostId={HOST_ID} />)
    expect(screen.getByText('No sessions on this host')).toBeInTheDocument()
  })

  it('renders session table with name, mode, cwd columns', () => {
    render(<SessionsSection hostId={HOST_ID} />)
    // Column headers
    expect(screen.getByText('Session Name')).toBeInTheDocument()
    expect(screen.getByText('Mode')).toBeInTheDocument()
    expect(screen.getByText('CWD')).toBeInTheDocument()
    // Session data
    expect(screen.getByText('dev')).toBeInTheDocument()
    expect(screen.getByText('terminal')).toBeInTheDocument()
    expect(screen.getByText('/tmp')).toBeInTheDocument()
  })

  it('shows "New Session" button enabled when online', () => {
    render(<SessionsSection hostId={HOST_ID} />)
    const btn = screen.getByRole('button', { name: /New Session/i })
    expect(btn).toBeInTheDocument()
    expect(btn).not.toBeDisabled()
  })

  it('shows "New Session" button disabled when offline', () => {
    useHostStore.setState({
      hosts: { [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
      hostOrder: [HOST_ID],
      runtime: { [HOST_ID]: { status: 'disconnected' } },
      activeHostId: HOST_ID,
    })
    render(<SessionsSection hostId={HOST_ID} />)
    const btn = screen.getByRole('button', { name: /New Session/i })
    expect(btn).toBeDisabled()
  })

  it('renders agent status badge when agentStatuses has entry for session', () => {
    const ck = compositeKey(HOST_ID, 'abc')
    useAgentStore.setState({ statuses: { [ck]: 'running' } })
    render(<SessionsSection hostId={HOST_ID} />)
    expect(screen.getByText('running')).toBeInTheDocument()
  })

  it('renders dash when no agent status for session', () => {
    render(<SessionsSection hostId={HOST_ID} />)
    // The "—" em-dash is shown when no agent status
    expect(screen.getByText('—')).toBeInTheDocument()
  })

  it('clicking Open calls openSingletonTab', () => {
    render(<SessionsSection hostId={HOST_ID} />)
    const openBtn = screen.getByTitle('Open')
    fireEvent.click(openBtn)
    expect(mockOpenSingletonTab).toHaveBeenCalledWith({
      kind: 'tmux-session',
      hostId: HOST_ID,
      sessionCode: 'abc',
      mode: 'terminal',
      cachedName: 'dev',
      tmuxInstance: '',
    })
    expect(mockSetActiveTab).toHaveBeenCalledWith('tab-1')
  })

  it('clicking Open carries the session generation into the pane', () => {
    useSessionStore.setState({
      sessions: { [HOST_ID]: [{ ...SESSIONS[0], tmux_instance: '222:2000' }] },
    })
    render(<SessionsSection hostId={HOST_ID} />)
    fireEvent.click(screen.getByTitle('Open'))
    expect(mockOpenSingletonTab).toHaveBeenCalledWith(
      expect.objectContaining({ tmuxInstance: '222:2000' }),
    )
  })
})

describe('SessionsSection — v1 QuickCommandMenu removal (Phase 1c, Finding 4)', () => {
  beforeEach(() => {
    // Real store + real module-registry: feed a global command so the v1 menu's
    // useCommands() returns a non-empty list and the trigger button would
    // render IF the integration were still wired. Removing that wiring is what
    // this RED → GREEN test gates.
    useQuickCommandStore.setState({
      global: [{ id: 'cmd-row', name: 'RowCmd', command: 'echo r' }],
      byHost: {},
      bindings: {},
    })
  })

  it('does NOT render v1 QuickCommandMenu inside session rows (Phase 1c — moved to new-session adjacency)', () => {
    render(<SessionsSection hostId={HOST_ID} />)
    // v1 QuickCommandMenu trigger uses title="Quick Commands"; testing-library
    // falls back to title for accessible name when aria-label is absent.
    expect(screen.queryAllByRole('button', { name: /quick commands/i }).length).toBe(0)
    // Double-safeguard via title queryByTitle (covers any aria fallback edge cases).
    expect(screen.queryByTitle('Quick Commands')).toBeNull()
  })
})

describe('SessionsSection — host quick actions slot adjacent to new-session button (Phase 1c)', () => {
  beforeEach(() => {
    useQuickCommandStore.setState({
      global: [{ id: 'cmd-h', name: 'HostCmd', command: 'echo h' }],
      byHost: {},
      bindings: { 'cmd-h': [QUICK_COMMAND_SLOTS.HOST_ACTIONS] },
    })
    // Default createSession return value (override in individual tests as needed).
    vi.mocked(createSession).mockResolvedValue({
      code: 'sess-h',
      name: 'HostCmd',
      cwd: '~',
      mode: 'terminal',
    } as Awaited<ReturnType<typeof createSession>>)
    vi.mocked(executeCommand).mockResolvedValue(undefined)
  })

  it('renders <CommandSlot mountTo=HOST_ACTIONS> chips next to the new-session button', () => {
    render(<SessionsSection hostId={HOST_ID} />)
    expect(screen.getByLabelText(/^HostCmd/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /new session/i })).toBeInTheDocument()
  })

  it('hides slot when no commands are bound to HOST_ACTIONS (new-session button still visible)', () => {
    useQuickCommandStore.setState({
      global: [],
      byHost: {},
      bindings: {},
    })
    render(<SessionsSection hostId={HOST_ID} />)
    expect(screen.queryByLabelText(/^HostCmd/)).toBeNull()
    expect(screen.getByRole('button', { name: /new session/i })).toBeInTheDocument()
  })

  // Finding 5 — chip disabled when host disconnected (mirrors row actions /
  // new-session button semantics).
  it('disables HOST_ACTIONS chip when host runtime is disconnected (Finding 5)', () => {
    useHostStore.setState({
      hosts: { [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
      hostOrder: [HOST_ID],
      runtime: { [HOST_ID]: { status: 'disconnected' } },
      activeHostId: HOST_ID,
    })
    render(<SessionsSection hostId={HOST_ID} />)
    expect(screen.getByLabelText(/^HostCmd/)).toBeDisabled()
  })

  // Finding 3 — fast double-click suppression. Without an executor-level
  // busy ref the second click slips between React event ticks and creates
  // a second session.
  it('suppresses fast double-click on the chip — only one createSession call (Finding 3)', async () => {
    let resolveCreate: ((value: unknown) => void) | null = null
    vi.mocked(createSession).mockImplementation(
      () => new Promise((r) => { resolveCreate = r as (v: unknown) => void }),
    )
    render(<SessionsSection hostId={HOST_ID} />)
    const chip = screen.getByLabelText(/^HostCmd/)
    fireEvent.click(chip)
    fireEvent.click(chip) // second click during in-flight createSession
    expect(createSession).toHaveBeenCalledTimes(1)
    await act(async () => {
      resolveCreate?.({ code: 'sess-h', name: 'HostCmd', cwd: '~', mode: 'terminal' })
      await Promise.resolve()
    })
  })

  // Finding 2 — workspace-aware switchToSession. Host page IS in a workspace
  // → insertTab targets that workspace explicitly (not stale activeWorkspaceId).
  it('switchToSession inserts new tab into the workspace containing the host page (Finding 2)', async () => {
    // findWorkspaceByTab returns a workspace whose tabs include 'tab-host'
    mockFindWorkspaceByTab.mockReturnValue({ id: 'ws-with-host', name: 'A', tabs: ['tab-host'], activeTabId: 'tab-host' } as never)

    render(<SessionsSection hostId={HOST_ID} />)
    await act(async () => {
      fireEvent.click(screen.getByLabelText(/^HostCmd/))
      // flush createSession + executeCommand microtasks
      await Promise.resolve()
      await Promise.resolve()
      await Promise.resolve()
    })

    // ws-with-host (the host page's actual workspace), NOT a stale activeWorkspaceId
    expect(mockFindWorkspaceByTab).toHaveBeenCalledWith('tab-host')
    expect(mockInsertTab).toHaveBeenCalledWith(expect.any(String), 'ws-with-host')
  })

  // Finding 2 — host page is standalone (no workspace owns its tab) → pass
  // null explicitly so insertTab does NOT fall back to activeWorkspaceId.
  it('switchToSession passes null when host page is standalone (Finding 2)', async () => {
    mockFindWorkspaceByTab.mockReturnValue(null)

    render(<SessionsSection hostId={HOST_ID} />)
    await act(async () => {
      fireEvent.click(screen.getByLabelText(/^HostCmd/))
      await Promise.resolve()
      await Promise.resolve()
      await Promise.resolve()
    })

    // explicit null — NOT a stale activeWorkspaceId fallback
    expect(mockInsertTab).toHaveBeenCalledWith(expect.any(String), null)
  })

  // R1 fix (codex PR review round 1) — workspace snapshot must be taken at
  // click time, NOT inside switchToSession's closure (which fires after
  // createSession + executeCommand resolve). Otherwise a user who switches
  // to another tab during the in-flight pipeline would have the new session
  // routed by `findWorkspaceByTab` against a different tab id.
  it('snapshots owning workspace at click time, not after createSession resolves (R1 race)', async () => {
    // findWorkspaceByTab resolves DIFFERENTLY depending on which tab id is
    // queried. Old buggy code would call this with 'tab-other' (post-await
    // value) and route to ws-B; fixed code calls with 'tab-host' (click-time
    // value) and routes to ws-A.
    mockFindWorkspaceByTab.mockImplementation((tabId: string) => {
      if (tabId === 'tab-host') return { id: 'ws-A', name: 'A', tabs: ['tab-host'], activeTabId: 'tab-host' } as never
      if (tabId === 'tab-other') return { id: 'ws-B', name: 'B', tabs: ['tab-other'], activeTabId: 'tab-other' } as never
      return null
    })

    let resolveCreate: ((value: unknown) => void) | null = null
    vi.mocked(createSession).mockImplementation(
      () => new Promise((r) => { resolveCreate = r as (v: unknown) => void }),
    )

    render(<SessionsSection hostId={HOST_ID} />)
    fireEvent.click(screen.getByLabelText(/^HostCmd/))

    // User switches to another tab while createSession is still pending.
    mockActiveTabId = 'tab-other'

    await act(async () => {
      resolveCreate?.({ code: 'sess-h', name: 'A', cwd: '~', mode: 'terminal' })
      await Promise.resolve()
      await Promise.resolve()
      await Promise.resolve()
    })

    // The lookup must have used 'tab-host' (the click-time active tab),
    // NOT 'tab-other' (the now-current tab). insertTab must target ws-A.
    expect(mockFindWorkspaceByTab).toHaveBeenCalledWith('tab-host')
    expect(mockFindWorkspaceByTab).not.toHaveBeenCalledWith('tab-other')
    expect(mockInsertTab).toHaveBeenCalledWith(expect.any(String), 'ws-A')
    expect(mockInsertTab).not.toHaveBeenCalledWith(expect.any(String), 'ws-B')
  })

  // Finding 1 (smoke test for the wiring; full assertHostLive coverage lives
  // in slot-executor.test.ts). Confirm SessionsSection passes a probe that
  // returns false for an absent host.
  it('assertHostLive caller returns false when host record is removed mid-flight (Finding 1)', async () => {
    let resolveCreate: ((value: unknown) => void) | null = null
    vi.mocked(createSession).mockImplementation(
      () => new Promise((r) => { resolveCreate = r as (v: unknown) => void }),
    )

    render(<SessionsSection hostId={HOST_ID} />)
    fireEvent.click(screen.getByLabelText(/^HostCmd/))

    // Simulate Settings-side host deletion BEFORE createSession resolves
    useHostStore.setState({ hosts: {}, hostOrder: [], runtime: {}, activeHostId: null })
    await act(async () => {
      resolveCreate?.({ code: 'sess-h', name: 'HostCmd', cwd: '~', mode: 'terminal' })
      await Promise.resolve()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(executeCommand).not.toHaveBeenCalled() // command did NOT ship to fallback host
  })
})
