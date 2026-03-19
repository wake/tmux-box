import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { TabBar } from './TabBar'
import type { Tab } from '../types/tab'

const mockTabs: Tab[] = [
  { id: 't1', type: 'terminal', label: 'dev-server', icon: 'Terminal', hostId: 'mlab', sessionName: 'dev' },
  { id: 't2', type: 'stream', label: 'claude', icon: 'ChatCircleDots', hostId: 'mlab', sessionName: 'claude' },
  { id: 't3', type: 'editor', label: 'App.tsx', icon: 'File', hostId: 'mlab', filePath: '/App.tsx', isDirty: true },
]

describe('TabBar', () => {
  it('renders all tabs', () => {
    cleanup()
    render(
      <TabBar tabs={mockTabs} activeTabId="t1" onSelectTab={vi.fn()} onCloseTab={vi.fn()} onAddTab={vi.fn()} />,
    )
    expect(screen.getByText('dev-server')).toBeTruthy()
    expect(screen.getByText('claude')).toBeTruthy()
    expect(screen.getByText('App.tsx')).toBeTruthy()
  })

  it('highlights active tab', () => {
    cleanup()
    render(
      <TabBar tabs={mockTabs} activeTabId="t1" onSelectTab={vi.fn()} onCloseTab={vi.fn()} onAddTab={vi.fn()} />,
    )
    const activeTab = screen.getByText('dev-server').closest('button')!
    expect(activeTab.className).toContain('border-b')
  })

  it('calls onSelectTab on click', () => {
    cleanup()
    const onSelect = vi.fn()
    render(
      <TabBar tabs={mockTabs} activeTabId="t1" onSelectTab={onSelect} onCloseTab={vi.fn()} onAddTab={vi.fn()} />,
    )
    fireEvent.click(screen.getByText('claude'))
    expect(onSelect).toHaveBeenCalledWith('t2')
  })

  it('calls onCloseTab on close button click', () => {
    cleanup()
    const onClose = vi.fn()
    render(
      <TabBar tabs={mockTabs} activeTabId="t1" onSelectTab={vi.fn()} onCloseTab={onClose} onAddTab={vi.fn()} />,
    )
    const closeButtons = screen.getAllByTitle('關閉分頁')
    fireEvent.click(closeButtons[0])
    expect(onClose).toHaveBeenCalledWith('t1')
  })

  it('shows dirty indicator for modified editor tabs', () => {
    cleanup()
    render(
      <TabBar tabs={mockTabs} activeTabId="t1" onSelectTab={vi.fn()} onCloseTab={vi.fn()} onAddTab={vi.fn()} />,
    )
    const dirtyTab = screen.getByText('App.tsx').closest('button')!
    expect(dirtyTab.textContent).toContain('●')
  })
})
