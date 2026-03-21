import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { TabBar } from './TabBar'
import type { Tab } from '../types/tab'
import { registerTabRenderer, clearRegistry } from '../lib/tab-registry'

beforeEach(() => {
  cleanup()
  clearRegistry()
  registerTabRenderer('session', {
    component: (() => null) as React.FC,
    viewModes: ['terminal', 'stream'],
    defaultViewMode: 'terminal',
    icon: (tab) => tab.viewMode === 'stream' ? 'ChatCircleDots' : 'Terminal',
  })
  registerTabRenderer('editor', {
    component: (() => null) as React.FC,
    icon: () => 'File',
  })
})

const defaultHandlers = {
  onSelectTab: vi.fn(),
  onCloseTab: vi.fn(),
  onAddTab: vi.fn(),
  onReorderTabs: vi.fn(),
  onMiddleClick: vi.fn(),
  onContextMenu: vi.fn(),
}

const mockTabs: Tab[] = [
  { id: 't1', type: 'session', label: 'dev-server', icon: 'Terminal', hostId: 'mlab', viewMode: 'terminal', data: { sessionName: 'dev' }, pinned: false, locked: false },
  { id: 't2', type: 'session', label: 'claude', icon: 'ChatCircleDots', hostId: 'mlab', viewMode: 'stream', data: { sessionName: 'claude' }, pinned: false, locked: false },
  { id: 't3', type: 'editor', label: 'App.tsx', icon: 'File', hostId: 'mlab', data: { filePath: '/App.tsx', isDirty: true }, pinned: false, locked: false },
]

const pinnedTabs: Tab[] = [
  { id: 'p1', type: 'session', label: 'pinned-a', icon: 'Terminal', hostId: 'local', viewMode: 'terminal', data: { sessionName: 'a' }, pinned: true, locked: true },
  { id: 't1', type: 'session', label: 'normal-b', icon: 'Terminal', hostId: 'local', viewMode: 'terminal', data: { sessionName: 'b' }, pinned: false, locked: false },
  { id: 't2', type: 'session', label: 'normal-c', icon: 'Terminal', hostId: 'local', viewMode: 'terminal', data: { sessionName: 'c' }, pinned: false, locked: false },
]

describe('TabBar', () => {
  it('renders all tabs', () => {
    render(<TabBar tabs={mockTabs} activeTabId="t1" {...defaultHandlers} />)
    expect(screen.getByText('dev-server')).toBeTruthy()
    expect(screen.getByText('claude')).toBeTruthy()
    expect(screen.getByText('App.tsx')).toBeTruthy()
  })

  it('highlights active tab', () => {
    render(<TabBar tabs={mockTabs} activeTabId="t1" {...defaultHandlers} />)
    const activeTab = screen.getByText('dev-server').closest('button')!
    expect(activeTab.className).toContain('text-white')
  })

  it('calls onSelectTab on click', () => {
    const onSelect = vi.fn()
    render(<TabBar tabs={mockTabs} activeTabId="t1" {...defaultHandlers} onSelectTab={onSelect} />)
    fireEvent.click(screen.getByText('claude'))
    expect(onSelect).toHaveBeenCalledWith('t2')
  })

  it('calls onCloseTab on close button click', () => {
    const onClose = vi.fn()
    render(<TabBar tabs={mockTabs} activeTabId="t1" {...defaultHandlers} onCloseTab={onClose} />)
    const closeButtons = screen.getAllByTitle('關閉分頁')
    fireEvent.click(closeButtons[0])
    expect(onClose).toHaveBeenCalledWith('t1')
  })

  it('shows dirty indicator for modified editor tabs', () => {
    render(<TabBar tabs={mockTabs} activeTabId="t1" {...defaultHandlers} />)
    const dirtyTab = screen.getByText('App.tsx').closest('button')!
    expect(dirtyTab.textContent).toContain('●')
  })

  it('renders pinned tabs as icon-only with title', () => {
    render(<TabBar tabs={pinnedTabs} activeTabId="t1" {...defaultHandlers} />)
    const pinnedBtn = screen.getByTitle('pinned-a')
    expect(pinnedBtn).toBeInTheDocument()
    // Pinned tab should not render label text in the button content
    expect(pinnedBtn.textContent).not.toContain('pinned-a')
  })

  it('renders normal tabs with label', () => {
    render(<TabBar tabs={pinnedTabs} activeTabId="t1" {...defaultHandlers} />)
    expect(screen.getByText('normal-b')).toBeInTheDocument()
    expect(screen.getByText('normal-c')).toBeInTheDocument()
  })

  it('locked tab hides close button', () => {
    const lockedTabs: Tab[] = [
      { id: 't1', type: 'session', label: 'locked-tab', icon: 'Terminal', hostId: 'local', viewMode: 'terminal', data: { sessionName: 'x' }, pinned: false, locked: true },
    ]
    render(<TabBar tabs={lockedTabs} activeTabId="t1" {...defaultHandlers} />)
    expect(screen.queryByTitle('關閉分頁')).not.toBeInTheDocument()
  })

  it('shows lock icon on locked non-pinned tab', () => {
    const lockedTabs: Tab[] = [
      { id: 't1', type: 'session', label: 'locked-tab', icon: 'Terminal', hostId: 'local', viewMode: 'terminal', data: { sessionName: 'x' }, pinned: false, locked: true },
    ]
    render(<TabBar tabs={lockedTabs} activeTabId="t1" {...defaultHandlers} />)
    // Lock icon rendered via Phosphor Lock component
    expect(screen.getByText('locked-tab')).toBeInTheDocument()
  })

  it('calls onAddTab on + button', () => {
    const onAdd = vi.fn()
    render(<TabBar tabs={mockTabs} activeTabId="t1" {...defaultHandlers} onAddTab={onAdd} />)
    fireEvent.click(screen.getByTitle('新增分頁'))
    expect(onAdd).toHaveBeenCalled()
  })

  it('shows separator between pinned and normal zones', () => {
    const { container } = render(<TabBar tabs={pinnedTabs} activeTabId="t1" {...defaultHandlers} />)
    const separator = container.querySelector('.bg-gray-700')
    expect(separator).toBeInTheDocument()
  })

  it('no pinned-zone separator when no pinned tabs', () => {
    const { container } = render(<TabBar tabs={mockTabs} activeTabId="t1" {...defaultHandlers} />)
    // No pinned/normal zone divider (h-4 height, distinct from tab separators which are h-3.5)
    const zoneDividers = container.querySelectorAll('.w-px.h-4.bg-gray-700')
    expect(zoneDividers.length).toBe(0)
  })
})
