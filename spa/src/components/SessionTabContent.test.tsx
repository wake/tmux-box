import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { SessionTabContent } from './SessionTabContent'
import type { Tab } from '../types/tab'

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

const makeTab = (viewMode: string): Tab => ({
  id: 't1', type: 'session', label: 'dev', icon: 'Terminal', hostId: 'mlab',
  pinned: false, locked: false, viewMode, data: { sessionName: 'dev-server', sessionCode: 'dev001' },
})

describe('SessionTabContent', () => {
  it('renders TerminalView when viewMode is terminal', () => {
    render(<SessionTabContent tab={makeTab('terminal')} isActive={true} wsBase="ws://test" daemonBase="http://test" />)
    expect(screen.getByTestId('terminal-view')).toBeTruthy()
    expect(screen.queryByTestId('conversation-view')).toBeNull()
  })

  it('renders ConversationView when viewMode is stream', () => {
    render(<SessionTabContent tab={makeTab('stream')} isActive={true} wsBase="ws://test" daemonBase="http://test" />)
    expect(screen.getByTestId('conversation-view')).toBeTruthy()
    expect(screen.queryByTestId('terminal-view')).toBeNull()
  })

  it('defaults to terminal when viewMode is undefined', () => {
    const tab: Tab = { id: 't1', type: 'session', label: 'dev', icon: 'Terminal', hostId: 'mlab', pinned: false, locked: false, data: { sessionName: 'dev-server', sessionCode: 'dev001' } }
    render(<SessionTabContent tab={tab} isActive={true} wsBase="ws://test" daemonBase="http://test" />)
    expect(screen.getByTestId('terminal-view')).toBeTruthy()
  })
})
