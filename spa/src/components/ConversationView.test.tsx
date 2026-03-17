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
  it('renders empty state when connected', () => {
    render(<ConversationView wsUrl="ws://test" sessionName="test" />)
    // After mount, clear() runs and resets handoffState to idle.
    // Set connected after mount.
    act(() => {
      useStreamStore.getState().setHandoffState('connected')
    })
    expect(screen.getByText(/waiting/i)).toBeInTheDocument()
  })

  it('renders messages', () => {
    render(<ConversationView wsUrl="ws://test" sessionName="test" />)
    act(() => {
      useStreamStore.getState().setHandoffState('connected')
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

  it('shows ThinkingIndicator when streaming with no assistant messages', () => {
    render(<ConversationView wsUrl="ws://test" sessionName="test" />)
    act(() => {
      useStreamStore.getState().setHandoffState('connected')
      useStreamStore.getState().setStreaming(true)
    })
    expect(screen.getByTestId('thinking-indicator')).toBeInTheDocument()
  })

  it('hides ThinkingIndicator when assistant message arrives', () => {
    render(<ConversationView wsUrl="ws://test" sessionName="test" />)
    act(() => {
      useStreamStore.getState().setHandoffState('connected')
      useStreamStore.getState().setStreaming(true)
      useStreamStore.getState().addMessage({
        type: 'assistant',
        message: {
          role: 'assistant',
          content: [{ type: 'text', text: 'Reply' }],
          stop_reason: null,
        },
      })
    })
    expect(screen.queryByTestId('thinking-indicator')).not.toBeInTheDocument()
  })

  it('shows HandoffButton when handoffState is idle', () => {
    // clear() in useEffect sets handoffState to idle, so it shows HandoffButton by default
    render(<ConversationView wsUrl="ws://test" sessionName="test" presetName="cc" />)
    expect(screen.getByText('Start cc')).toBeInTheDocument()
  })

  it('shows HandoffButton when handoffState is disconnected', () => {
    render(<ConversationView wsUrl="ws://test" sessionName="test" presetName="cc" />)
    act(() => {
      useStreamStore.getState().setHandoffState('disconnected')
    })
    expect(screen.getByText(/disconnected/i)).toBeInTheDocument()
  })

  it('hides HandoffButton when handoffState is connected', () => {
    render(<ConversationView wsUrl="ws://test" sessionName="test" presetName="cc" />)
    act(() => {
      useStreamStore.getState().setHandoffState('connected')
    })
    expect(screen.queryByText('Start cc')).not.toBeInTheDocument()
    expect(screen.getByText(/waiting/i)).toBeInTheDocument()
  })
})
