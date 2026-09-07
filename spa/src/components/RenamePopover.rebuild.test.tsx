// spa/src/components/RenamePopover.rebuild.test.tsx — the tab-name popover as
// a per-pane rebuild editor (spec §4.10, plan Task 15).
//
// Two halves: the entry point (a real double-click on a tab whose PRIMARY pane
// is not a live terminal must still open the popover) and the content (one
// block per terminal pane, each editing its own pane's record, and none of it
// hijacking the Enter that submits a rename).
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, waitFor, act } from '@testing-library/react'
import { useTabStore } from '../stores/useTabStore'
import { useResumeTemplateStore } from '../stores/useResumeTemplateStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useHostStore } from '../stores/useHostStore'
import { useAgentStore } from '../stores/useAgentStore'
import { clearModuleRegistry, registerModule } from '../lib/module-registry'
import { collectRenameTargets, collectRenameEntryPanes, useTabWorkspaceActions } from '../features/workspace/hooks'
import type { PaneContent, PaneRebuildRecord, Tab } from '../types/tab'

const mockOnPointerDown = vi.fn()

vi.mock('@dnd-kit/sortable', () => ({
  useSortable: () => ({
    attributes: {},
    listeners: { onPointerDown: mockOnPointerDown },
    setNodeRef: vi.fn(),
    transform: null,
    transition: null,
    isDragging: false,
  }),
}))

vi.mock('../features/workspace/lib/icon-path-cache', () => ({
  getIconPath: () => null,
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))

const renameSession = vi.fn(async () => new Response('{}', { status: 200 }))
vi.mock('../lib/host-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/host-api')>()),
  renameSession: (...args: unknown[]) => renameSession(...(args as [])),
}))

// Lazy imports so the mocks above are applied first.
const { RenamePopover } = await import('./RenamePopover')
const { SortableTab } = await import('./SortableTab')

const record: PaneRebuildRecord = {
  sessionName: 'dev',
  tmuxInstance: '111:1000',
  cwd: '/w/p',
  agent: { type: 'cc', sessionId: 'S1', updatedAt: 1 },
  capturedAt: 1,
}

function seedSplitTab(contents: PaneContent[]): Tab {
  const panes = contents.map((content, i) => ({ id: `p${i + 1}`, content }))
  return {
    id: 't1',
    pinned: false,
    locked: false,
    createdAt: 0,
    layout: panes.length === 1
      ? { type: 'leaf' as const, pane: panes[0] }
      : {
          type: 'split' as const,
          id: 's1',
          direction: 'h' as const,
          children: panes.map((pane) => ({ type: 'leaf' as const, pane })),
          sizes: panes.map(() => 100 / panes.length),
        },
  }
}

const terminal = (over: Partial<Extract<PaneContent, { kind: 'tmux-session' }>> = {}): PaneContent => ({
  kind: 'tmux-session',
  hostId: 'h1',
  sessionCode: 'abc123',
  mode: 'terminal',
  cachedName: 'dev',
  tmuxInstance: '111:1000',
  rebuild: record,
  ...over,
})

const props = {
  anchorRect: { left: 0, top: 0, width: 100, height: 20, bottom: 20, right: 100 } as DOMRect,
  currentName: 'dev',
  onConfirm: vi.fn(async () => {}),
  onCancel: vi.fn(),
}

/** App's wiring, minus everything the popover does not touch. */
function TabBarHarness() {
  const tabs = useTabStore((s) => s.tabs)
  const displayTabs = Object.values(tabs)
  const a = useTabWorkspaceActions(displayTabs)
  return (
    <>
      {displayTabs.map((tab) => (
        <SortableTab
          key={tab.id}
          tab={tab}
          isActive
          onSelect={() => {}}
          onClose={() => {}}
          onMiddleClick={() => {}}
          onContextMenu={() => {}}
          onRename={(tabId) => { const t = tabs[tabId]; if (t) a.openRenameForTab(t) }}
        />
      ))}
      {a.renameTarget && (
        <RenamePopover
          anchorRect={a.renameTarget.anchorRect}
          currentName={a.renameTarget.currentName}
          onConfirm={a.handleRenameConfirm}
          onCancel={a.handleRenameCancel}
          error={a.renameError}
          onClearError={a.handleClearRenameError}
          tab={tabs[a.renameTarget.tabId]}
          onRenamePane={a.handleRenamePane}
          onEditRebuildField={a.handleEditRebuildField}
        />
      )}
    </>
  )
}

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
  clearModuleRegistry()
  registerModule({ id: 'session', name: 'Session', panes: [{ kind: 'tmux-session', component: () => null }] })
  registerModule({ id: 'editor', name: 'Editor', panes: [{ kind: 'editor', component: () => null }] })
  useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
  useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
  useHostStore.setState({ runtime: {} })
  useAgentStore.setState({ unread: {}, statuses: {}, subagents: {} })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  useResumeTemplateStore.setState({ agents: {} })
})

afterEach(() => cleanup())

describe('collectRenameTargets', () => {
  it('collects one target per terminal pane, skipping the editor', () => {
    const tab = seedSplitTab([
      { kind: 'editor', source: { type: 'local' }, filePath: '/a.md' },
      terminal(),
    ])
    const targets = collectRenameTargets(tab)
    expect(targets).toHaveLength(1)
    expect(targets[0].sessionCode).toBe('abc123')
    expect(targets[0].paneId).toBe('p2')
  })

  it('includes a terminated pane', () => {
    const tab = seedSplitTab([terminal({ terminated: 'tmux-restarted' })])
    expect(collectRenameTargets(tab)).toHaveLength(1)
    expect(collectRenameTargets(tab)[0].terminated).toBe('tmux-restarted')
  })

  it('excludes stream panes', () => {
    const tab = seedSplitTab([terminal({ mode: 'stream' })])
    expect(collectRenameTargets(tab)).toHaveLength(0)
  })

  it('returns nothing for a tab with no tmux pane', () => {
    const tab = seedSplitTab([{ kind: 'editor', source: { type: 'local' }, filePath: '/a.md' }])
    expect(collectRenameTargets(tab)).toHaveLength(0)
  })
})

describe('popover entry point', () => {
  it('opens on a real double-click when the primary pane is an editor', async () => {
    const tab = seedSplitTab([
      { kind: 'editor', source: { type: 'local' }, filePath: '/a.md' },
      terminal(),
    ])
    useTabStore.setState({ tabs: { t1: tab }, tabOrder: ['t1'], activeTabId: 't1' })
    render(<TabBarHarness />)
    fireEvent.doubleClick(screen.getByRole('tab'))
    expect(await screen.findByDisplayValue('dev')).toBeInTheDocument()
  })

  it('opens on a real double-click when the primary pane is terminated', async () => {
    const tab = seedSplitTab([terminal({ terminated: 'tmux-restarted' })])
    useTabStore.setState({ tabs: { t1: tab }, tabOrder: ['t1'], activeTabId: 't1' })
    render(<TabBarHarness />)
    fireEvent.doubleClick(screen.getByRole('tab'))
    expect(await screen.findByDisplayValue('dev')).toBeInTheDocument()
  })

  it('stays closed for a tab with no terminal pane', () => {
    const tab = seedSplitTab([{ kind: 'editor', source: { type: 'local' }, filePath: '/a.md' }])
    useTabStore.setState({ tabs: { t1: tab }, tabOrder: ['t1'], activeTabId: 't1' })
    render(<TabBarHarness />)
    fireEvent.doubleClick(screen.getByRole('tab'))
    expect(screen.queryByDisplayValue('dev')).not.toBeInTheDocument()
  })
})

describe('per-pane detail blocks', () => {
  it('renders one block per terminal pane with independent targets', () => {
    const tab = seedSplitTab([
      terminal({ sessionCode: 'aaa', cachedName: 'one', rebuild: { ...record, sessionName: 'one', cwd: '/w/one' } }),
      terminal({ sessionCode: 'bbb', cachedName: 'two', rebuild: { ...record, sessionName: 'two', cwd: '/w/two' } }),
    ])
    render(<RenamePopover {...props} tab={tab} />)
    expect(screen.getByDisplayValue('one')).toBeInTheDocument()
    expect(screen.getByDisplayValue('two')).toBeInTheDocument()
    expect(screen.getByText('/w/one')).toBeInTheDocument()
    expect(screen.getByText('/w/two')).toBeInTheDocument()
  })

  it('shows the three rebuild fields for a live pane', () => {
    const tab = seedSplitTab([terminal()])
    render(<RenamePopover {...props} tab={tab} />)
    expect(screen.getByDisplayValue('dev')).toBeInTheDocument()
    expect(screen.getByText('/w/p')).toBeInTheDocument()
    expect(screen.getByText('claude --resume S1')).toBeInTheDocument()
  })

  it('routes a live pane name row through the daemon rename', () => {
    const onRenamePane = vi.fn(async () => {})
    const onEditRebuildField = vi.fn()
    const tab = seedSplitTab([terminal()])
    render(<RenamePopover {...props} tab={tab} onRenamePane={onRenamePane} onEditRebuildField={onEditRebuildField} />)
    const name = screen.getByDisplayValue('dev')
    fireEvent.change(name, { target: { value: 'renamed' } })
    fireEvent.keyDown(name, { key: 'Enter' })
    expect(onRenamePane).toHaveBeenCalledWith(expect.objectContaining({ paneId: 'p1', sessionCode: 'abc123' }), 'renamed')
    expect(onEditRebuildField).not.toHaveBeenCalled()
  })

  it('a dead pane name row edits the record and sends no rename', () => {
    const onRenamePane = vi.fn(async () => {})
    const onEditRebuildField = vi.fn()
    const tab = seedSplitTab([terminal({ terminated: 'tmux-restarted' })])
    render(<RenamePopover {...props} tab={tab} onRenamePane={onRenamePane} onEditRebuildField={onEditRebuildField} />)
    const name = screen.getByDisplayValue('dev')
    fireEvent.change(name, { target: { value: 'revived' } })
    fireEvent.keyDown(name, { key: 'Enter' })
    expect(onRenamePane).not.toHaveBeenCalled()
    expect(onEditRebuildField).toHaveBeenCalledWith(
      expect.objectContaining({ paneId: 'p1' }), 'sessionName', 'revived',
    )
  })

  it('edits cwd on a live pane into the record only', () => {
    const onRenamePane = vi.fn(async () => {})
    const onEditRebuildField = vi.fn()
    const tab = seedSplitTab([terminal()])
    render(<RenamePopover {...props} tab={tab} onRenamePane={onRenamePane} onEditRebuildField={onEditRebuildField} />)
    fireEvent.doubleClick(screen.getByText('/w/p'))
    const input = screen.getByDisplayValue('/w/p')
    fireEvent.change(input, { target: { value: '/w/other' } })
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(onEditRebuildField).toHaveBeenCalledWith(expect.objectContaining({ paneId: 'p1' }), 'cwd', '/w/other')
    expect(onRenamePane).not.toHaveBeenCalled()
  })

  it('edits the resume command into the record as an override', () => {
    const onEditRebuildField = vi.fn()
    const tab = seedSplitTab([terminal()])
    render(<RenamePopover {...props} tab={tab} onEditRebuildField={onEditRebuildField} />)
    // The row shows the RESOLVED command, which for this record is the cc
    // template composed with S1 — nothing is stored on the record itself.
    fireEvent.doubleClick(screen.getByText('claude --resume S1'))
    const input = screen.getByDisplayValue('claude --resume S1')
    fireEvent.change(input, { target: { value: 'cld-yolo --resume S2' } })
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(onEditRebuildField).toHaveBeenCalledWith(
      expect.objectContaining({ paneId: 'p1' }), 'resumeCommandOverride', 'cld-yolo --resume S2',
    )
  })

  it('re-renders the resume row when the template changes, without a remount', () => {
    const tab = seedSplitTab([terminal()])
    render(<RenamePopover {...props} tab={tab} onEditRebuildField={vi.fn()} />)
    expect(screen.getByText('claude --resume S1')).toBeInTheDocument()
    act(() => { useResumeTemplateStore.getState().setTemplate('cc', 'exact', 'cld-yolo --resume {id}') })
    expect(screen.getByText('cld-yolo --resume S1')).toBeInTheDocument()
  })

  // The popover's own Enter handler sits on the container, so an Enter that
  // escapes a nested editor would submit the rename. Asserting only on
  // onConfirm would pass for the wrong reason (the legacy draft never changes
  // in tab mode), so this pins the propagation itself with an outer listener.
  it('editing cwd does not submit the rename', () => {
    const outer = vi.fn()
    const onConfirm = vi.fn(async () => {})
    const onRenamePane = vi.fn(async () => {})
    const tab = seedSplitTab([terminal()])
    render(
      <div onKeyDown={outer}>
        <RenamePopover {...props} tab={tab} onConfirm={onConfirm} onRenamePane={onRenamePane} />
      </div>,
    )
    fireEvent.doubleClick(screen.getByText('/w/p'))
    const cwd = screen.getByDisplayValue('/w/p')

    // Control: an unrelated key does reach the outer listener.
    fireEvent.keyDown(cwd, { key: 'a' })
    expect(outer).toHaveBeenCalledTimes(1)

    fireEvent.keyDown(cwd, { key: 'Enter' })
    expect(outer).toHaveBeenCalledTimes(1)
    expect(onConfirm).not.toHaveBeenCalled()
    expect(onRenamePane).not.toHaveBeenCalled()
  })

  it('the resume editor does not let Enter escape either', () => {
    const outer = vi.fn()
    const tab = seedSplitTab([terminal()])
    render(
      <div onKeyDown={outer}>
        <RenamePopover {...props} tab={tab} />
      </div>,
    )
    fireEvent.doubleClick(screen.getByText('claude --resume S1'))
    fireEvent.keyDown(screen.getByDisplayValue('claude --resume S1'), { key: 'Enter' })
    expect(outer).not.toHaveBeenCalled()
  })

  it('a name row Enter does not escape to the legacy submit path', () => {
    const outer = vi.fn()
    const tab = seedSplitTab([terminal()])
    render(
      <div onKeyDown={outer}>
        <RenamePopover {...props} tab={tab} onRenamePane={vi.fn(async () => {})} />
      </div>,
    )
    const name = screen.getByDisplayValue('dev')
    fireEvent.change(name, { target: { value: 'renamed' } })
    fireEvent.keyDown(name, { key: 'Enter' })
    expect(outer).not.toHaveBeenCalled()
  })

  it('keeps Escape cancelling the popover', () => {
    const onCancel = vi.fn()
    const tab = seedSplitTab([terminal()])
    render(<RenamePopover {...props} tab={tab} onCancel={onCancel} />)
    fireEvent.keyDown(screen.getByDisplayValue('dev'), { key: 'Escape' })
    expect(onCancel).toHaveBeenCalled()
  })

  it('rejects an invalid session name on a live pane', () => {
    const onRenamePane = vi.fn(async () => {})
    const tab = seedSplitTab([terminal()])
    render(<RenamePopover {...props} tab={tab} onRenamePane={onRenamePane} />)
    const name = screen.getByDisplayValue('dev')
    fireEvent.change(name, { target: { value: 'bad name!' } })
    fireEvent.keyDown(name, { key: 'Enter' })
    expect(onRenamePane).not.toHaveBeenCalled()
    expect(screen.getByText(/only letters|僅允許/i)).toBeInTheDocument()
  })

  it('falls back to the pane name when no record was ever captured', () => {
    const tab = seedSplitTab([terminal({ rebuild: undefined })])
    render(<RenamePopover {...props} tab={tab} />)
    expect(screen.getByDisplayValue('dev')).toBeInTheDocument()
  })
})

describe('hook write-through', () => {
  it('handleEditRebuildField writes only the edited pane record', () => {
    const tab = seedSplitTab([
      terminal({ sessionCode: 'aaa', cachedName: 'one', rebuild: { ...record, sessionName: 'one' } }),
      terminal({ sessionCode: 'bbb', cachedName: 'two', rebuild: { ...record, sessionName: 'two' } }),
    ])
    useTabStore.setState({ tabs: { t1: tab }, tabOrder: ['t1'], activeTabId: 't1' })
    render(<TabBarHarness />)
    fireEvent.doubleClick(screen.getByRole('tab'))
    fireEvent.doubleClick(screen.getAllByText('/w/p')[0])
    const input = screen.getByDisplayValue('/w/p')
    fireEvent.change(input, { target: { value: '/w/edited' } })
    fireEvent.keyDown(input, { key: 'Enter' })

    const layout = useTabStore.getState().tabs.t1.layout
    const leaves = layout.type === 'split' ? layout.children.map((c) => (c.type === 'leaf' ? c.pane : null)) : []
    const first = leaves[0]!.content as Extract<PaneContent, { kind: 'tmux-session' }>
    const second = leaves[1]!.content as Extract<PaneContent, { kind: 'tmux-session' }>
    expect(first.rebuild?.cwd).toBe('/w/edited')
    expect(second.rebuild?.cwd).toBe('/w/p')
  })
})

// A stream pane has no rebuild record and no rebuild fields, but it is still a
// tmux session with a name — and renaming it by double-clicking the tab is an
// entry point that predates this feature. Restricting the popover's entry
// condition to terminal panes silently removed it.
describe('stream panes keep the legacy rename', () => {
  it('collects a live stream pane as an entry pane, but never as a detail target', () => {
    const tab = seedSplitTab([terminal({ mode: 'stream' })])
    expect(collectRenameTargets(tab)).toHaveLength(0)
    expect(collectRenameEntryPanes(tab)).toHaveLength(1)
    expect(collectRenameEntryPanes(tab)[0].mode).toBe('stream')
  })

  it('does not offer a terminated stream pane, which has nothing to rename', () => {
    const tab = seedSplitTab([terminal({ mode: 'stream', terminated: 'tmux-restarted' })])
    expect(collectRenameEntryPanes(tab)).toHaveLength(0)
  })

  it('opens the single rename input on a stream-only tab and renames through the daemon', async () => {
    const tab = seedSplitTab([terminal({ mode: 'stream' })])
    useTabStore.setState({ tabs: { t1: tab }, tabOrder: ['t1'], activeTabId: 't1' })
    render(<TabBarHarness />)
    fireEvent.doubleClick(screen.getByRole('tab'))

    const input = await screen.findByDisplayValue('dev')
    // The rebuild fields belong to terminal panes only.
    expect(screen.queryByTestId('rename-pane-block-p1')).toBeNull()

    fireEvent.change(input, { target: { value: 'renamed' } })
    fireEvent.keyDown(input, { key: 'Enter' })
    await waitFor(() => expect(renameSession).toHaveBeenCalledWith('h1', 'abc123', 'renamed'))
  })

  it('still shows the detail blocks when a terminal pane shares the tab', async () => {
    const tab = seedSplitTab([terminal({ mode: 'stream', sessionCode: 'str1' }), terminal({ sessionCode: 'trm1' })])
    useTabStore.setState({ tabs: { t1: tab }, tabOrder: ['t1'], activeTabId: 't1' })
    render(<TabBarHarness />)
    fireEvent.doubleClick(screen.getByRole('tab'))
    expect(await screen.findByTestId('rename-pane-block-p2')).toBeInTheDocument()
    expect(screen.queryByTestId('rename-pane-block-p1')).toBeNull()
  })
})
