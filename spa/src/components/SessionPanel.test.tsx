// spa/src/components/SessionPanel.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import SessionPanel from './SessionPanel'
import { useSessionStore } from '../stores/useSessionStore'

vi.mock('../lib/api', () => ({
  listSessions: vi.fn().mockResolvedValue([]),
}))

beforeEach(() => {
  cleanup()
  useSessionStore.setState({ sessions: [], activeId: null })
})

describe('SessionPanel', () => {
  it('shows empty state', () => {
    render(<SessionPanel />)
    expect(screen.getByText('No sessions')).toBeInTheDocument()
  })

  it('renders session list', () => {
    useSessionStore.setState({
      sessions: [
        { id: 1, uid: 'test0001', name: 'dev', tmux_target: 'dev:0', cwd: '/tmp', mode: 'term', group_id: 0, sort_order: 0, cc_session_id: '', cc_model: '', has_relay: false },
        { id: 2, uid: 'test0002', name: 'prod', tmux_target: 'prod:0', cwd: '/tmp', mode: 'stream', group_id: 0, sort_order: 0, cc_session_id: '', cc_model: '', has_relay: false },
      ],
      activeId: null,
    })
    render(<SessionPanel />)
    expect(screen.getByText('dev')).toBeInTheDocument()
    expect(screen.getByText('prod')).toBeInTheDocument()
  })

  it('highlights active session', () => {
    useSessionStore.setState({
      sessions: [
        { id: 1, uid: 'test0001', name: 'dev', tmux_target: 'dev:0', cwd: '/tmp', mode: 'term', group_id: 0, sort_order: 0, cc_session_id: '', cc_model: '', has_relay: false },
      ],
      activeId: 1,
    })
    render(<SessionPanel />)
    const btn = screen.getByRole('button', { name: /dev/i })
    expect(btn.className).toContain('bg-gray-800')
  })

  it('sets active on click', () => {
    const setActive = vi.fn()
    useSessionStore.setState({
      sessions: [
        { id: 1, uid: 'test0001', name: 'dev', tmux_target: 'dev:0', cwd: '/tmp', mode: 'term', group_id: 0, sort_order: 0, cc_session_id: '', cc_model: '', has_relay: false },
      ],
      activeId: null,
      setActive,
    })
    render(<SessionPanel />)
    fireEvent.click(screen.getByRole('button', { name: /dev/i }))
    expect(setActive).toHaveBeenCalledWith(1)
  })

  it('shows terminal icon for term mode', () => {
    useSessionStore.setState({
      sessions: [
        { id: 1, uid: 'test0001', name: 'dev', tmux_target: 'dev:0', cwd: '/tmp', mode: 'term', group_id: 0, sort_order: 0, cc_session_id: '', cc_model: '', has_relay: false },
      ],
      activeId: null,
    })
    render(<SessionPanel />)
    // Terminal icon should be present (Phosphor Terminal icon)
    expect(screen.getByTestId('session-icon-1')).toBeInTheDocument()
  })
})
