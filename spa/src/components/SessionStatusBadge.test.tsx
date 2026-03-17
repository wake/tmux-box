// spa/src/components/SessionStatusBadge.test.tsx
import { describe, it, expect } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import SessionStatusBadge, { type SessionStatus } from './SessionStatusBadge'

describe('SessionStatusBadge', () => {
  const cases: [SessionStatus, string][] = [
    ['normal', 'bg-gray-500'],
    ['not-in-cc', 'bg-gray-500'],
    ['cc-idle', 'bg-emerald-700'],
    ['cc-running', 'bg-green-400'],
    ['cc-waiting', 'bg-yellow-400'],
    ['cc-unread', 'bg-blue-400'],
  ]

  cases.forEach(([status, expectedClass]) => {
    it(`renders ${expectedClass} for status "${status}"`, () => {
      cleanup()
      render(<SessionStatusBadge status={status} />)
      const badge = screen.getByTestId('status-badge')
      expect(badge.className).toContain(expectedClass)
      expect(badge).toHaveAttribute('title', status)
    })
  })

  it('renders as a small dot (w-2 h-2 rounded-full)', () => {
    cleanup()
    render(<SessionStatusBadge status="normal" />)
    const badge = screen.getByTestId('status-badge')
    expect(badge.className).toContain('w-2')
    expect(badge.className).toContain('h-2')
    expect(badge.className).toContain('rounded-full')
  })
})
