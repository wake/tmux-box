// spa/src/components/ConversationView.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, act } from '@testing-library/react'
import ConversationView from './ConversationView'
import { useStreamStore } from '../stores/useStreamStore'

// No WS mock needed — ConversationView no longer manages WS connections

const SESSION = 'test-session'

const emptyState = {
  sessions: {},
  sessionStatus: {},
  relayStatus: {},
  handoffState: {},
  handoffProgress: {},
}

beforeEach(() => {
  cleanup()
  useStreamStore.setState(emptyState)
})

describe('ConversationView', () => {
  it('renders empty state when connected', () => {
    render(<ConversationView sessionName={SESSION} />)
    act(() => {
      useStreamStore.getState().setHandoffState(SESSION, 'connected')
    })
    expect(screen.getByText(/waiting/i)).toBeInTheDocument()
  })

  it('renders messages from per-session store', () => {
    render(<ConversationView sessionName={SESSION} />)
    act(() => {
      useStreamStore.getState().setHandoffState(SESSION, 'connected')
      useStreamStore.getState().addMessage(SESSION, {
        type: 'assistant',
        message: {
          role: 'assistant',
          content: [{ type: 'text', text: 'Hello from Claude' }],
          stop_reason: 'end_turn',
        },
      } as any)
    })
    expect(screen.getByText('Hello from Claude')).toBeInTheDocument()
  })

  it('shows ThinkingIndicator when streaming with no assistant messages', () => {
    render(<ConversationView sessionName={SESSION} />)
    act(() => {
      useStreamStore.getState().setHandoffState(SESSION, 'connected')
      useStreamStore.getState().setStreaming(SESSION, true)
    })
    expect(screen.getByTestId('thinking-indicator')).toBeInTheDocument()
  })

  it('hides ThinkingIndicator when assistant message arrives', () => {
    render(<ConversationView sessionName={SESSION} />)
    act(() => {
      useStreamStore.getState().setHandoffState(SESSION, 'connected')
      useStreamStore.getState().setStreaming(SESSION, true)
      useStreamStore.getState().addMessage(SESSION, {
        type: 'assistant',
        message: {
          role: 'assistant',
          content: [{ type: 'text', text: 'Reply' }],
          stop_reason: null,
        },
      } as any)
    })
    expect(screen.queryByTestId('thinking-indicator')).not.toBeInTheDocument()
  })

  it('shows HandoffButton when handoffState is idle', () => {
    useStreamStore.getState().setSessionStatus(SESSION, 'cc-idle')
    render(<ConversationView sessionName={SESSION} />)
    expect(screen.getByText('Handoff')).toBeInTheDocument()
  })

  it('shows HandoffButton when handoffState is disconnected', () => {
    render(<ConversationView sessionName={SESSION} />)
    act(() => {
      useStreamStore.getState().setHandoffState(SESSION, 'disconnected')
    })
    expect(screen.getByText(/disconnected/i)).toBeInTheDocument()
  })

  it('hides HandoffButton when handoffState is connected', () => {
    render(<ConversationView sessionName={SESSION} />)
    act(() => {
      useStreamStore.getState().setHandoffState(SESSION, 'connected')
    })
    expect(screen.queryByText('Handoff')).not.toBeInTheDocument()
    expect(screen.getByText(/waiting/i)).toBeInTheDocument()
  })
})

describe('ConversationView message rendering', () => {
  it('renders thinking block for assistant thinking content', () => {
    useStreamStore.setState({
      sessions: {
        [SESSION]: {
          messages: [{
            type: 'assistant',
            message: {
              role: 'assistant',
              content: [
                { type: 'thinking', thinking: 'Let me analyze...' },
                { type: 'text', text: 'Here is my answer.' },
              ],
              stop_reason: 'end_turn',
            },
          }],
          pendingControlRequests: [],
          isStreaming: false,
          conn: null,
          sessionInfo: { ccSessionId: '', model: '' },
          cost: 0,
        },
      },
      handoffState: { [SESSION]: 'connected' },
    })

    render(<ConversationView sessionName={SESSION} />)

    expect(screen.getByTestId('thinking-header')).toBeInTheDocument()
    expect(screen.getByText('Here is my answer.')).toBeInTheDocument()
  })

  it('renders tool_result block for user tool results', () => {
    useStreamStore.setState({
      sessions: {
        [SESSION]: {
          messages: [{
            type: 'user',
            message: {
              role: 'user',
              content: [
                { type: 'tool_result', tool_use_id: 'toolu_01', content: 'file contents here', is_error: false },
              ],
              stop_reason: null,
            },
          }],
          pendingControlRequests: [],
          isStreaming: false,
          conn: null,
          sessionInfo: { ccSessionId: '', model: '' },
          cost: 0,
        },
      },
      handoffState: { [SESSION]: 'connected' },
    })

    render(<ConversationView sessionName={SESSION} />)

    expect(screen.getByTestId('tool-result-header')).toBeInTheDocument()
  })

  it('renders interrupted message with prohibit style', () => {
    useStreamStore.setState({
      sessions: {
        [SESSION]: {
          messages: [{
            type: 'user',
            message: {
              role: 'user',
              content: [{ type: 'text', text: '[Request interrupted by user]' }],
              stop_reason: null,
            },
          }],
          pendingControlRequests: [],
          isStreaming: false,
          conn: null,
          sessionInfo: { ccSessionId: '', model: '' },
          cost: 0,
        },
      },
      handoffState: { [SESSION]: 'connected' },
    })

    render(<ConversationView sessionName={SESSION} />)

    expect(screen.getByTestId('interrupted-msg')).toBeInTheDocument()
  })

  it('renders slash command with command bubble style', () => {
    useStreamStore.setState({
      sessions: {
        [SESSION]: {
          messages: [{
            type: 'user',
            message: {
              role: 'user',
              content: [{ type: 'text', text: '/exit' }],
              stop_reason: null,
            },
          }],
          pendingControlRequests: [],
          isStreaming: false,
          conn: null,
          sessionInfo: { ccSessionId: '', model: '' },
          cost: 0,
        },
      },
      handoffState: { [SESSION]: 'connected' },
    })

    render(<ConversationView sessionName={SESSION} />)

    expect(screen.getByTestId('command-bubble')).toBeInTheDocument()
    expect(screen.getByTestId('command-bubble')).toHaveTextContent('/exit')
  })

  it('renders mixed assistant content blocks correctly', () => {
    useStreamStore.setState({
      sessions: {
        [SESSION]: {
          messages: [{
            type: 'assistant',
            message: {
              role: 'assistant',
              content: [
                { type: 'thinking', thinking: 'Deep thought' },
                { type: 'text', text: 'My response' },
                { type: 'tool_use', id: 'toolu_01', name: 'Bash', input: { command: 'ls' } },
              ],
              stop_reason: 'end_turn',
            },
          }],
          pendingControlRequests: [],
          isStreaming: false,
          conn: null,
          sessionInfo: { ccSessionId: '', model: '' },
          cost: 0,
        },
      },
      handoffState: { [SESSION]: 'connected' },
    })

    render(<ConversationView sessionName={SESSION} />)

    expect(screen.getByTestId('thinking-header')).toBeInTheDocument()
    expect(screen.getByText('My response')).toBeInTheDocument()
    expect(screen.getByText('Bash')).toBeInTheDocument()
    expect(screen.getByText('ls')).toBeInTheDocument()
  })

  it('renders user tool_result with error state', () => {
    useStreamStore.setState({
      sessions: {
        [SESSION]: {
          messages: [{
            type: 'user',
            message: {
              role: 'user',
              content: [
                { type: 'tool_result', tool_use_id: 'toolu_02', content: 'ENOENT: no such file', is_error: true },
              ],
              stop_reason: null,
            },
          }],
          pendingControlRequests: [],
          isStreaming: false,
          conn: null,
          sessionInfo: { ccSessionId: '', model: '' },
          cost: 0,
        },
      },
      handoffState: { [SESSION]: 'connected' },
    })

    render(<ConversationView sessionName={SESSION} />)

    const block = screen.getByTestId('tool-result-block')
    expect(block.className).toContain('border-[#302a2a]')
  })
})
