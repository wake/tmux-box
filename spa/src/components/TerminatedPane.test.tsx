import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
import { TerminatedPane } from './TerminatedPane'
import { useTabStore } from '../stores/useTabStore'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { findPane } from '../lib/pane-tree'
import type { PaneContent, Tab } from '../types/tab'

vi.mock('./SessionPickerList', () => ({
  SessionPickerList: ({ onSelect }: { onSelect: (sel: unknown) => void }) => (
    <button
      data-testid="session-picker"
      onClick={() =>
        onSelect({
          hostId: 'new-host',
          sessionCode: 'new001',
          cachedName: 'new-session',
          tmuxInstance: 'tmux:inst',
        })
      }
    >
      Mock SessionPickerList
    </button>
  ),
}))

const TAB_ID = 'tab-1'
const PANE_ID = 'pane-1'

function makeContent(reason: 'session-closed' | 'tmux-restarted' | 'host-removed', mode: 'terminal' | 'stream' = 'terminal'): Extract<PaneContent, { kind: 'tmux-session' }> {
  return {
    kind: 'tmux-session',
    hostId: 'host-1',
    sessionCode: 'dev001',
    mode,
    cachedName: 'my-session',
    tmuxInstance: '123:456',
    terminated: reason,
  }
}

function setupTab(content: PaneContent) {
  const tab: Tab = {
    id: TAB_ID,
    pinned: false,
    locked: false,
    createdAt: Date.now(),
    layout: { type: 'leaf', pane: { id: PANE_ID, content } },
  }
  useTabStore.setState({
    tabs: { [TAB_ID]: tab },
    tabOrder: [TAB_ID],
    activeTabId: TAB_ID,
  })
}

beforeEach(() => {
  cleanup()
  useHostStore.setState({ hosts: {}, hostOrder: [], runtime: {}, activeHostId: null })
  useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
})

describe('TerminatedPane', () => {
  it('shows session-closed message', () => {
    const content = makeContent('session-closed')
    setupTab(content)
    render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)
    expect(screen.getByText('Session closed')).toBeInTheDocument()
    expect(screen.getByText('my-session no longer exists')).toBeInTheDocument()
  })

  it('shows tmux-restarted message', () => {
    const content = makeContent('tmux-restarted')
    setupTab(content)
    render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)
    expect(screen.getByText('tmux restarted')).toBeInTheDocument()
    expect(screen.getByText('Previous sessions are no longer valid')).toBeInTheDocument()
  })

  it('shows host-removed message', () => {
    const content = makeContent('host-removed')
    setupTab(content)
    render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)
    expect(screen.getByText('Host removed')).toBeInTheDocument()
    expect(screen.getByText('This host has been removed')).toBeInTheDocument()
  })

  it('has a close tab button that calls closeTab', () => {
    const content = makeContent('session-closed')
    setupTab(content)
    useWorkspaceStore.getState().reset()
    const ws = useWorkspaceStore.getState().addWorkspace('Test')
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, TAB_ID)
    render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)

    const closeBtn = screen.getByText('Close tab')
    expect(closeBtn).toBeInTheDocument()

    fireEvent.click(closeBtn)
    // Tab should be closed
    expect(useTabStore.getState().tabs[TAB_ID]).toBeUndefined()
  })

  it('session selection calls setPaneContent with correct data, preserving mode', () => {
    const content = makeContent('session-closed', 'stream')
    setupTab(content)
    render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)

    fireEvent.click(screen.getByTestId('session-picker'))

    const updatedTab = useTabStore.getState().tabs[TAB_ID]
    expect(updatedTab).toBeDefined()
    if (updatedTab.layout.type === 'leaf') {
      const newContent = updatedTab.layout.pane.content
      expect(newContent).toEqual({
        kind: 'tmux-session',
        hostId: 'new-host',
        sessionCode: 'new001',
        mode: 'stream', // preserved from original tab
        cachedName: 'new-session',
        tmuxInstance: 'tmux:inst',
      })
    }
  })

  it('session selection for terminal mode tab preserves terminal mode', () => {
    const content = makeContent('session-closed', 'terminal')
    setupTab(content)
    render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)

    fireEvent.click(screen.getByTestId('session-picker'))

    const updatedTab = useTabStore.getState().tabs[TAB_ID]
    if (updatedTab.layout.type === 'leaf') {
      const newContent = updatedTab.layout.pane.content
      expect(newContent.kind).toBe('tmux-session')
      if (newContent.kind === 'tmux-session') {
        expect(newContent.mode).toBe('terminal')
      }
    }
  })

  it('renders the SessionPickerList', () => {
    const content = makeContent('session-closed')
    setupTab(content)
    render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)
    expect(screen.getByTestId('session-picker')).toBeInTheDocument()
  })
})

describe('TerminatedPane rebuild action set', () => {
  function setupSplitTab(contents: Array<{ id: string; content: PaneContent }>) {
    const tab: Tab = {
      id: TAB_ID,
      pinned: false,
      locked: false,
      createdAt: Date.now(),
      layout: {
        type: 'split',
        id: 'split-1',
        direction: 'h',
        sizes: [50, 50],
        children: contents.map(({ id, content }) => ({ type: 'leaf' as const, pane: { id, content } })),
      },
    }
    useTabStore.setState({ tabs: { [TAB_ID]: tab }, tabOrder: [TAB_ID], activeTabId: TAB_ID })
  }

  function paneRebuild(paneId: string) {
    const layout = useTabStore.getState().tabs[TAB_ID].layout
    const pane = findPane(layout, paneId)
    const c = pane?.content
    return c && c.kind === 'tmux-session' ? c.rebuild : undefined
  }

  it('renders the action set on a terminal pane, seeded from the cached name', () => {
    const content = makeContent('tmux-restarted')
    setupTab(content)
    render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)
    expect(screen.getByTestId('rebuild-action-set')).toBeInTheDocument()
    expect(screen.getByTestId('rebuild-session-name-cell')).toHaveTextContent('my-session')
    // The pane is dead, so the create row is locked on.
    const create = screen.getByRole('checkbox', { name: 'Create tmux session' })
    expect(create).toBeChecked()
    expect(create).toBeDisabled()
  })

  it('omits the action set on a stream pane', () => {
    const content = makeContent('tmux-restarted', 'stream')
    setupTab(content)
    render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)
    expect(screen.queryByTestId('rebuild-action-set')).toBeNull()
  })

  it('hides Rebuild when the host is gone', () => {
    const content = makeContent('host-removed')
    setupTab(content)
    render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)
    expect(screen.queryByRole('button', { name: 'Rebuild' })).toBeNull()
    expect(screen.getByTestId('rebuild-host-removed-hint')).toBeInTheDocument()
  })

  it('an edit lands on this pane only, not on its split sibling', () => {
    const rebuild = {
      sessionName: 'my-session',
      tmuxInstance: '123:456',
      cwd: '/w/p',
      resumeCommand: 'claude --resume S1',
      capturedAt: 1,
    }
    const content = { ...makeContent('tmux-restarted'), rebuild }
    const sibling = { ...makeContent('tmux-restarted'), rebuild: { ...rebuild } }
    setupSplitTab([{ id: PANE_ID, content }, { id: 'pane-2', content: sibling }])
    render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)

    fireEvent.doubleClick(screen.getByTestId('rebuild-cwd-cell'))
    const input = screen.getByTestId('rebuild-cwd-input')
    fireEvent.change(input, { target: { value: '/w/edited' } })
    fireEvent.keyDown(input, { key: 'Enter' })

    expect(paneRebuild(PANE_ID)?.cwd).toBe('/w/edited')
    expect(paneRebuild('pane-2')?.cwd).toBe('/w/p')
  })
})
