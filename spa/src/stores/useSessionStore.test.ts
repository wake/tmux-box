// spa/src/stores/useSessionStore.test.ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useSessionStore } from './useSessionStore'

vi.mock('../lib/api', () => ({
  listSessions: vi.fn().mockResolvedValue([
    { code: 'abc123', name: 'test', cwd: '/tmp', mode: 'term', cc_session_id: '', cc_model: '', has_relay: false },
  ]),
}))

beforeEach(() => {
  // Reset zustand store between tests
  useSessionStore.setState({ sessions: [], activeId: null })
})

describe('useSessionStore', () => {
  it('fetches sessions', async () => {
    const { result } = renderHook(() => useSessionStore())
    await act(async () => { await result.current.fetch('http://localhost:7860') })
    expect(result.current.sessions).toHaveLength(1)
    expect(result.current.sessions[0].name).toBe('test')
  })

  it('sets active session', () => {
    const { result } = renderHook(() => useSessionStore())
    act(() => { result.current.setActive('abc123') })
    expect(result.current.activeId).toBe('abc123')
  })

  it('persists to localStorage', () => {
    const { result } = renderHook(() => useSessionStore())
    act(() => { result.current.setActive('abc123') })
    // Zustand persist middleware writes to localStorage
    const stored = JSON.parse(localStorage.getItem('tbox-sessions') || '{}')
    expect(stored.state?.activeId).toBe('abc123')
  })
})
