import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { TabContent } from './TabContent'
import type { Tab } from '../types/tab'

// Mock heavy components
vi.mock('./TerminalView', () => ({
  default: ({ wsUrl }: { wsUrl: string }) => (
    <div data-testid="terminal-view">Terminal: {wsUrl}</div>
  ),
}))
vi.mock('./ConversationView', () => ({
  default: ({ sessionName }: { sessionName: string }) => (
    <div data-testid="conversation-view">Stream: {sessionName}</div>
  ),
}))

beforeEach(() => cleanup())

const termTab: Tab = { id: 't1', type: 'terminal', label: 'dev', icon: 'Terminal', hostId: 'mlab', sessionName: 'dev' }
const streamTab: Tab = { id: 't2', type: 'stream', label: 'claude', icon: 'ChatCircleDots', hostId: 'mlab', sessionName: 'claude' }
const editorTab: Tab = { id: 't3', type: 'editor', label: 'file.ts', icon: 'File', hostId: 'mlab', filePath: '/file.ts' }

describe('TabContent', () => {
  it('renders TerminalView for terminal tab', () => {
    render(<TabContent activeTab={termTab} wsBase="ws://test" />)
    expect(screen.getByTestId('terminal-view')).toBeTruthy()
    expect(screen.queryByTestId('conversation-view')).toBeNull()
  })

  it('renders ConversationView for stream tab', () => {
    render(<TabContent activeTab={streamTab} wsBase="ws://test" />)
    expect(screen.getByTestId('conversation-view')).toBeTruthy()
  })

  it('renders placeholder for editor tab', () => {
    render(<TabContent activeTab={editorTab} wsBase="ws://test" />)
    expect(screen.getByText(/file\.ts/)).toBeTruthy()
  })

  it('renders empty state when no active tab', () => {
    render(<TabContent activeTab={null} wsBase="ws://test" />)
    expect(screen.getByText(/選擇或建立/)).toBeTruthy()
  })

  it('only mounts active tab — no keep-alive', () => {
    const { rerender } = render(<TabContent activeTab={termTab} wsBase="ws://test" />)
    expect(screen.getByTestId('terminal-view')).toBeTruthy()

    rerender(<TabContent activeTab={streamTab} wsBase="ws://test" />)
    expect(screen.queryByTestId('terminal-view')).toBeNull()
    expect(screen.getByTestId('conversation-view')).toBeTruthy()
  })
})
