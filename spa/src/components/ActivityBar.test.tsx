import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { ActivityBar } from './ActivityBar'
import type { Workspace } from '../types/tab'

const mockWorkspaces: Workspace[] = [
  { id: 'ws-1', name: 'Project A', color: '#7a6aaa', icon: '🔧', tabs: ['t1', 't2'], activeTabId: 't1', directories: [], sidebarState: { zones: { 'left-outer': { width: 200, mode: 'default' }, 'left-inner': { width: 200, mode: 'default' }, 'right-inner': { width: 200, mode: 'default' }, 'right-outer': { width: 200, mode: 'default' } } } },
  { id: 'ws-2', name: 'Server', color: '#6aaa7a', icon: '🖥', tabs: ['t3'], activeTabId: 't3', directories: [], sidebarState: { zones: { 'left-outer': { width: 200, mode: 'default' }, 'left-inner': { width: 200, mode: 'default' }, 'right-inner': { width: 200, mode: 'default' }, 'right-outer': { width: 200, mode: 'default' } } } },
]

const mockStandaloneTabs = [
  { id: 'st-1', type: 'session' as const, label: 'misc', icon: 'Terminal', hostId: 'mlab', pinned: false, locked: false, data: {} },
]

describe('ActivityBar', () => {
  it('renders workspace icons', () => {
    cleanup()
    render(
      <ActivityBar
        workspaces={mockWorkspaces}
        standaloneTabs={mockStandaloneTabs}
        activeWorkspaceId="ws-1"
        activeStandaloneTabId={null}
        onSelectWorkspace={vi.fn()}
        onSelectStandaloneTab={vi.fn()}
        onAddWorkspace={vi.fn()}
        onOpenSettings={vi.fn()}
      />,
    )
    expect(screen.getByTitle('Project A')).toBeTruthy()
    expect(screen.getByTitle('Server')).toBeTruthy()
  })

  it('highlights active workspace', () => {
    cleanup()
    render(
      <ActivityBar
        workspaces={mockWorkspaces}
        standaloneTabs={mockStandaloneTabs}
        activeWorkspaceId="ws-1"
        activeStandaloneTabId={null}
        onSelectWorkspace={vi.fn()}
        onSelectStandaloneTab={vi.fn()}
        onAddWorkspace={vi.fn()}
        onOpenSettings={vi.fn()}
      />,
    )
    const activeBtn = screen.getByTitle('Project A')
    expect(activeBtn.className).toContain('ring')
  })

  it('calls onSelectWorkspace on click', () => {
    cleanup()
    const onSelect = vi.fn()
    render(
      <ActivityBar
        workspaces={mockWorkspaces}
        standaloneTabs={mockStandaloneTabs}
        activeWorkspaceId="ws-1"
        activeStandaloneTabId={null}
        onSelectWorkspace={onSelect}
        onSelectStandaloneTab={vi.fn()}
        onAddWorkspace={vi.fn()}
        onOpenSettings={vi.fn()}
      />,
    )
    fireEvent.click(screen.getByTitle('Server'))
    expect(onSelect).toHaveBeenCalledWith('ws-2')
  })

  it('renders standalone tabs below separator', () => {
    cleanup()
    render(
      <ActivityBar
        workspaces={mockWorkspaces}
        standaloneTabs={mockStandaloneTabs}
        activeWorkspaceId="ws-1"
        activeStandaloneTabId={null}
        onSelectWorkspace={vi.fn()}
        onSelectStandaloneTab={vi.fn()}
        onAddWorkspace={vi.fn()}
        onOpenSettings={vi.fn()}
      />,
    )
    expect(screen.getByTitle('misc')).toBeTruthy()
  })
})
