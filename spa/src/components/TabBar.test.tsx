import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { TabBar } from './TabBar'
import type { Tab } from '../types/tab'
import { registerTabRenderer, clearRegistry } from '../lib/tab-registry'

const mockTabs: Tab[] = [
  { id: 't1', type: 'session', label: 'dev-server', icon: 'Terminal', hostId: 'mlab', viewMode: 'terminal', data: { sessionName: 'dev' }, pinned: false, locked: false },
  { id: 't2', type: 'session', label: 'claude', icon: 'ChatCircleDots', hostId: 'mlab', viewMode: 'stream', data: { sessionName: 'claude' }, pinned: false, locked: false },
  { id: 't3', type: 'editor', label: 'App.tsx', icon: 'File', hostId: 'mlab', data: { filePath: '/App.tsx', isDirty: true }, pinned: false, locked: false },
]

describe('TabBar', () => {
  beforeEach(() => {
    cleanup()
    clearRegistry()
    registerTabRenderer('session', {
      component: (() => null) as any,
      viewModes: ['terminal', 'stream'],
      defaultViewMode: 'terminal',
      icon: (tab) => tab.viewMode === 'stream' ? 'ChatCircleDots' : 'Terminal',
    })
    registerTabRenderer('editor', {
      component: (() => null) as any,
      icon: () => 'File',
    })
  })

  it('renders all tabs', () => {
    render(
      <TabBar tabs={mockTabs} activeTabId="t1" onSelectTab={vi.fn()} onCloseTab={vi.fn()} onAddTab={vi.fn()} />,
    )
    expect(screen.getByText('dev-server')).toBeTruthy()
    expect(screen.getByText('claude')).toBeTruthy()
    expect(screen.getByText('App.tsx')).toBeTruthy()
  })

  it('highlights active tab', () => {
    render(
      <TabBar tabs={mockTabs} activeTabId="t1" onSelectTab={vi.fn()} onCloseTab={vi.fn()} onAddTab={vi.fn()} />,
    )
    const activeTab = screen.getByText('dev-server').closest('button')!
    expect(activeTab.className).toContain('border-b')
  })

  it('calls onSelectTab on click', () => {
    const onSelect = vi.fn()
    render(
      <TabBar tabs={mockTabs} activeTabId="t1" onSelectTab={onSelect} onCloseTab={vi.fn()} onAddTab={vi.fn()} />,
    )
    fireEvent.click(screen.getByText('claude'))
    expect(onSelect).toHaveBeenCalledWith('t2')
  })

  it('calls onCloseTab on close button click', () => {
    const onClose = vi.fn()
    render(
      <TabBar tabs={mockTabs} activeTabId="t1" onSelectTab={vi.fn()} onCloseTab={onClose} onAddTab={vi.fn()} />,
    )
    const closeButtons = screen.getAllByTitle('關閉分頁')
    fireEvent.click(closeButtons[0])
    expect(onClose).toHaveBeenCalledWith('t1')
  })

  it('shows dirty indicator for modified editor tabs', () => {
    render(
      <TabBar tabs={mockTabs} activeTabId="t1" onSelectTab={vi.fn()} onCloseTab={vi.fn()} onAddTab={vi.fn()} />,
    )
    const dirtyTab = screen.getByText('App.tsx').closest('button')!
    expect(dirtyTab.textContent).toContain('●')
  })
})
