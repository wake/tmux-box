// spa/src/components/SessionPicker.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { SessionPicker } from './SessionPicker'
import type { Session } from '../lib/api'

const mockSessions: Session[] = [
  { code: 'abc001', name: 'dev-server', mode: 'term', cwd: '/home', cc_session_id: '', cc_model: '', has_relay: false },
  { code: 'def002', name: 'claude-code', mode: 'stream', cwd: '/home', cc_session_id: '', cc_model: '', has_relay: true },
]

beforeEach(() => cleanup())

describe('SessionPicker', () => {
  it('renders session list', () => {
    render(
      <SessionPicker
        sessions={mockSessions}
        existingTabSessionNames={[]}
        onSelect={vi.fn()}
        onClose={vi.fn()}
      />,
    )
    expect(screen.getByText('dev-server')).toBeTruthy()
    expect(screen.getByText('claude-code')).toBeTruthy()
  })

  it('marks sessions that already have tabs', () => {
    render(
      <SessionPicker
        sessions={mockSessions}
        existingTabSessionNames={['dev-server']}
        onSelect={vi.fn()}
        onClose={vi.fn()}
      />,
    )
    const devItem = screen.getByText('dev-server').closest('button')!
    expect(devItem.textContent).toContain('已開啟')
  })

  it('calls onSelect with session info', () => {
    const onSelect = vi.fn()
    render(
      <SessionPicker
        sessions={mockSessions}
        existingTabSessionNames={[]}
        onSelect={onSelect}
        onClose={vi.fn()}
      />,
    )
    fireEvent.click(screen.getByText('dev-server'))
    expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ name: 'dev-server', mode: 'term' }))
  })

  it('filters sessions by search text', () => {
    render(
      <SessionPicker
        sessions={mockSessions}
        existingTabSessionNames={[]}
        onSelect={vi.fn()}
        onClose={vi.fn()}
      />,
    )
    const input = screen.getByPlaceholderText('搜尋 session...')
    fireEvent.change(input, { target: { value: 'claude' } })
    expect(screen.queryByText('dev-server')).toBeNull()
    expect(screen.getByText('claude-code')).toBeTruthy()
  })
})
