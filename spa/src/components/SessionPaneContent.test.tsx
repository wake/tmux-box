import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, cleanup, act } from '@testing-library/react'
import { SessionPaneContent } from './SessionPaneContent'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useTabStore } from '../stores/useTabStore'
import { useConfigStore } from '../stores/useConfigStore'
import { useWorkspaceStore } from '../features/workspace/store'
import type { Pane, Tab, Workspace } from '../types/tab'
import type { ConfigData } from '../lib/host-api'
import { probeSessionCwd } from '../lib/rebuild/cwd-probe'
import { probeSessionProvenance } from '../lib/rebuild/provenance-probe'

const terminalViewProps = vi.hoisted(() => ({ last: undefined as Record<string, unknown> | undefined }))

vi.mock('./TerminalView', () => ({
  default: (props: Record<string, unknown>) => {
    terminalViewProps.last = props
    return <div data-testid="terminal-view" />
  },
}))

vi.mock('./ConversationView', () => ({
  default: () => <div data-testid="conversation-view" />,
}))

vi.mock('./TerminatedPane', () => ({
  TerminatedPane: ({ content }: { content: { terminated: string } }) => (
    <div data-testid="terminated-pane">Terminated: {content.terminated}</div>
  ),
}))

vi.mock('../lib/rebuild/cwd-probe', () => ({ probeSessionCwd: vi.fn() }))
vi.mock('../lib/rebuild/provenance-probe', () => ({ probeSessionProvenance: vi.fn() }))

const HOST_ID = 'test-host'

const makePane = (overrides?: Partial<Pane>): Pane => ({
  id: 'pane-1',
  content: { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' },
  ...overrides,
})

const defaultConfig: ConfigData = {
  bind: '0.0.0.0',
  port: 7860,
  stream: { presets: [{ name: 'cc', command: 'claude -p' }] },
  detect: { cc_commands: [], poll_interval: 5 },
}

function setupTabStore(pane: Pane) {
  const tab: Tab = {
    id: 'tab-1',
    pinned: false,
    locked: false,
    createdAt: Date.now(),
    layout: { type: 'leaf', pane },
  }
  useTabStore.setState({
    tabs: { 'tab-1': tab },
    tabOrder: ['tab-1'],
    activeTabId: 'tab-1',
  })
}

function makeWorkspace(id: string, tabs: string[]): Workspace {
  return {
    id,
    name: id,
    tabs,
    activeTabId: tabs[0] ?? null,
  }
}

beforeEach(() => {
  cleanup()
  vi.mocked(probeSessionCwd).mockClear()
  vi.mocked(probeSessionProvenance).mockClear()
  terminalViewProps.last = undefined
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0 } },
    hostOrder: [HOST_ID],
    activeHostId: HOST_ID,
    // The attach gate is open by default here; the probe waits on it
    // (spec §4.6.2) and its own suite covers the closed case.
    runtime: { [HOST_ID]: { status: 'connected' as const, attachReady: true } },
  })
  useSessionStore.setState({
    sessions: {
      [HOST_ID]: [{
        code: 'dev001', name: 'dev001', cwd: '/tmp', mode: 'terminal',
        cc_session_id: '', cc_model: '', has_relay: false,
      }],
    },
    activeHostId: HOST_ID,
    activeCode: null,
  })
  useConfigStore.setState({ config: defaultConfig })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
})

describe('SessionPaneContent', () => {
  it('returns null for non-session pane content', () => {
    const pane: Pane = { id: 'pane-1', content: { kind: 'dashboard' } }
    setupTabStore(pane)
    const { container } = render(<SessionPaneContent pane={pane} isActive={true} />)
    expect(container.innerHTML).toBe('')
  })

  it('renders TerminalView for terminal mode', () => {
    const pane = makePane()
    setupTabStore(pane)
    render(<SessionPaneContent pane={pane} isActive={true} />)
    expect(screen.getByTestId('terminal-view')).toBeInTheDocument()
  })

  it('renders ConversationView for stream mode', () => {
    const pane = makePane({
      content: { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'stream', cachedName: '', tmuxInstance: '' },
    })
    setupTabStore(pane)
    render(<SessionPaneContent pane={pane} isActive={true} />)
    expect(screen.getByTestId('conversation-view')).toBeInTheDocument()
  })

  it('renders TerminatedPane when content.terminated is set', () => {
    const pane = makePane({
      content: {
        kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001',
        mode: 'terminal', cachedName: 'my-session', tmuxInstance: '',
        terminated: 'session-closed',
      },
    })
    setupTabStore(pane)
    render(<SessionPaneContent pane={pane} isActive={true} />)
    expect(screen.getByTestId('terminated-pane')).toBeInTheDocument()
    expect(screen.getByText('Terminated: session-closed')).toBeInTheDocument()
  })

  describe('TerminalView workspaceId plumbing (PR-5)', () => {
    it('passes workspaceId when pane belongs to a workspace tab', () => {
      const pane = makePane()
      setupTabStore(pane)
      useWorkspaceStore.setState({
        workspaces: [makeWorkspace('wsA', ['tab-1'])],
        activeWorkspaceId: 'wsA',
      })
      render(<SessionPaneContent pane={pane} isActive={true} />)
      expect(terminalViewProps.last?.workspaceId).toBe('wsA')
    })

    it('passes undefined workspaceId for standalone tab (not in any workspace)', () => {
      const pane = makePane()
      setupTabStore(pane)
      useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
      render(<SessionPaneContent pane={pane} isActive={true} />)
      expect(terminalViewProps.last?.workspaceId).toBeUndefined()
    })

    it('uses link-source workspace, not active workspace (multi-workspace isolation)', () => {
      const pane = makePane()
      setupTabStore(pane)
      useWorkspaceStore.setState({
        workspaces: [
          makeWorkspace('wsA', []),
          makeWorkspace('wsB', ['tab-1']),
        ],
        activeWorkspaceId: 'wsA',
      })
      render(<SessionPaneContent pane={pane} isActive={true} />)
      expect(terminalViewProps.last?.workspaceId).toBe('wsB')
    })
  })

  // A pane opened after the session list has settled gets no further
  // broadcast, so attach is the second cwd-probe trigger (spec §4.4).
  describe('cwd probe on attach', () => {
    it('probes the pane binding once when a terminal pane attaches', () => {
      const pane = makePane({
        content: {
          kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001',
          mode: 'terminal', cachedName: '', tmuxInstance: '222:2000',
        },
      })
      setupTabStore(pane)
      const view = render(<SessionPaneContent pane={pane} isActive={true} />)
      view.rerender(<SessionPaneContent pane={pane} isActive={false} />)
      expect(probeSessionCwd).toHaveBeenCalledTimes(1)
      expect(probeSessionCwd).toHaveBeenCalledWith(HOST_ID, 'dev001', '222:2000')
    })

    // The mount trigger must respect the same gate as the terminal attach
    // (spec §4.6.2): a connection whose first `sessions` payload has not
    // landed has not proved which generation the pane's code belongs to.
    it('does not probe while the host attach gate is closed', () => {
      useHostStore.setState({ runtime: { [HOST_ID]: { status: 'connected' as const, attachReady: false } } })
      const pane = makePane({
        content: {
          kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001',
          mode: 'terminal', cachedName: '', tmuxInstance: '222:2000',
        },
      })
      setupTabStore(pane)
      render(<SessionPaneContent pane={pane} isActive={true} />)
      expect(probeSessionCwd).not.toHaveBeenCalled()
    })

    it('probes as soon as the gate opens under the mounted pane', async () => {
      useHostStore.setState({ runtime: { [HOST_ID]: { status: 'connected' as const, attachReady: false } } })
      const pane = makePane({
        content: {
          kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001',
          mode: 'terminal', cachedName: '', tmuxInstance: '222:2000',
        },
      })
      setupTabStore(pane)
      render(<SessionPaneContent pane={pane} isActive={true} />)
      expect(probeSessionCwd).not.toHaveBeenCalled()

      await act(async () => {
        useHostStore.setState({ runtime: { [HOST_ID]: { status: 'connected' as const, attachReady: true } } })
      })
      expect(probeSessionCwd).toHaveBeenCalledWith(HOST_ID, 'dev001', '222:2000')
    })

    it('does not probe a stream-mode or terminated pane', () => {
      const stream = makePane({
        content: { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'stream', cachedName: '', tmuxInstance: '222:2000' },
      })
      setupTabStore(stream)
      render(<SessionPaneContent pane={stream} isActive={true} />)

      const dead = makePane({
        content: {
          kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001',
          mode: 'terminal', cachedName: '', tmuxInstance: '222:2000',
          terminated: 'session-closed',
        },
      })
      setupTabStore(dead)
      render(<SessionPaneContent pane={dead} isActive={true} />)

      expect(probeSessionCwd).not.toHaveBeenCalled()
    })
  })

  // The third provenance trigger (spec §5.4): a pane opened after the session
  // list has settled gets no further `sessions` sweep and, until its agent
  // speaks, no hook broadcast either — so attach is the only thing that can
  // ask on its behalf.
  describe('provenance probe on attach', () => {
    it('asks who owns the pane binding when a terminal pane attaches', () => {
      const pane = makePane({
        content: {
          kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001',
          mode: 'terminal', cachedName: '', tmuxInstance: '222:2000',
        },
      })
      setupTabStore(pane)
      const view = render(<SessionPaneContent pane={pane} isActive={true} />)
      view.rerender(<SessionPaneContent pane={pane} isActive={false} />)
      expect(probeSessionProvenance).toHaveBeenCalledTimes(1)
      expect(probeSessionProvenance).toHaveBeenCalledWith(HOST_ID, 'dev001', '222:2000')
    })

    it('does not ask while the host attach gate is closed', () => {
      useHostStore.setState({ runtime: { [HOST_ID]: { status: 'connected' as const, attachReady: false } } })
      const pane = makePane({
        content: {
          kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001',
          mode: 'terminal', cachedName: '', tmuxInstance: '222:2000',
        },
      })
      setupTabStore(pane)
      render(<SessionPaneContent pane={pane} isActive={true} />)
      expect(probeSessionProvenance).not.toHaveBeenCalled()
    })

    it('asks as soon as the gate opens under the mounted pane', async () => {
      useHostStore.setState({ runtime: { [HOST_ID]: { status: 'connected' as const, attachReady: false } } })
      const pane = makePane({
        content: {
          kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001',
          mode: 'terminal', cachedName: '', tmuxInstance: '222:2000',
        },
      })
      setupTabStore(pane)
      render(<SessionPaneContent pane={pane} isActive={true} />)
      expect(probeSessionProvenance).not.toHaveBeenCalled()

      await act(async () => {
        useHostStore.setState({ runtime: { [HOST_ID]: { status: 'connected' as const, attachReady: true } } })
      })
      expect(probeSessionProvenance).toHaveBeenCalledWith(HOST_ID, 'dev001', '222:2000')
    })

    it('does not ask for a stream-mode or terminated pane', () => {
      const stream = makePane({
        content: { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'stream', cachedName: '', tmuxInstance: '222:2000' },
      })
      setupTabStore(stream)
      render(<SessionPaneContent pane={stream} isActive={true} />)

      const dead = makePane({
        content: {
          kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001',
          mode: 'terminal', cachedName: '', tmuxInstance: '222:2000',
          terminated: 'session-closed',
        },
      })
      setupTabStore(dead)
      render(<SessionPaneContent pane={dead} isActive={true} />)

      expect(probeSessionProvenance).not.toHaveBeenCalled()
    })
  })

  it('does not render TerminalView when terminated', () => {
    const pane = makePane({
      content: {
        kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001',
        mode: 'terminal', cachedName: '', tmuxInstance: '',
        terminated: 'tmux-restarted',
      },
    })
    setupTabStore(pane)
    render(<SessionPaneContent pane={pane} isActive={true} />)
    expect(screen.queryByTestId('terminal-view')).not.toBeInTheDocument()
    expect(screen.getByTestId('terminated-pane')).toBeInTheDocument()
  })
})
