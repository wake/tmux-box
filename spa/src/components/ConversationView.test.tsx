// spa/src/components/ConversationView.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, act } from '@testing-library/react'
import ConversationView from './ConversationView'
import { useStreamStore } from '../stores/useStreamStore'

vi.mock('../lib/stream-ws', () => ({
  connectStream: vi.fn().mockReturnValue({
    send: vi.fn(),
    sendControlResponse: vi.fn(),
    interrupt: vi.fn(),
    close: vi.fn(),
  }),
  parseStreamMessage: vi.fn(),
}))

beforeEach(() => {
  cleanup()
  useStreamStore.getState().clear()
})

describe('ConversationView', () => {
  it('renders empty state', () => {
    render(<ConversationView wsUrl="ws://test" sessionName="test" />)
    expect(screen.getByText(/waiting/i)).toBeInTheDocument()
  })

  it('renders messages', () => {
    render(<ConversationView wsUrl="ws://test" sessionName="test" />)
    // Set state after mount so the useEffect clear() has already run
    act(() => {
      useStreamStore.getState().addMessage({
        type: 'assistant',
        message: {
          role: 'assistant',
          content: [{ type: 'text', text: 'Hello from Claude' }],
          stop_reason: 'end_turn',
        },
      })
    })
    expect(screen.getByText('Hello from Claude')).toBeInTheDocument()
  })
})
