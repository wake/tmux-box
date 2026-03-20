// spa/src/components/ConversationView.test.tsx
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, cleanup, act } from '@testing-library/react'
import ConversationView from './ConversationView'
import { useStreamStore } from '../stores/useStreamStore'
import type { StreamMessage } from '../lib/stream-ws'

// No WS mock needed — ConversationView no longer manages WS connections

const SESSION = 'test-session'

const emptyState = {
  sessions: {},
  sessionStatus: {},
  relayStatus: {},
  handoffProgress: {},
}

beforeEach(() => {
  cleanup()
  useStreamStore.setState(emptyState)
})

describe('ConversationView', () => {
  it('shows conversation UI when relay is connected', () => {
    render(<ConversationView sessionName={SESSION} />)
    act(() => {
      useStreamStore.getState().setRelayStatus(SESSION, true)
    })
    expect(screen.getByText(/waiting/i)).toBeInTheDocument()
    expect(screen.queryByText('Handoff')).not.toBeInTheDocument()
  })

  it('shows HandoffButton when relay is not connected', () => {
    useStreamStore.getState().setSessionStatus(SESSION, 'cc-idle')
    render(<ConversationView sessionName={SESSION} />)
    expect(screen.getByText('Handoff')).toBeInTheDocument()
  })

  it('shows progress when handoff is in progress', () => {
    render(<ConversationView sessionName={SESSION} />)
    act(() => {
      useStreamStore.getState().setHandoffProgress(SESSION, 'detecting')
    })
    expect(screen.getByText(/Detecting/i)).toBeInTheDocument()
  })

  it('renders messages when relay connected', () => {
    render(<ConversationView sessionName={SESSION} />)
    act(() => {
      useStreamStore.getState().setRelayStatus(SESSION, true)
      useStreamStore.getState().addMessage(SESSION, {
        type: 'assistant',
        message: {
          role: 'assistant',
          content: [{ type: 'text', text: 'Hello from Claude' }],
          stop_reason: 'end_turn',
        },
      } as StreamMessage)
    })
    expect(screen.getByText('Hello from Claude')).toBeInTheDocument()
  })

  it('shows ThinkingIndicator when streaming with no assistant messages', () => {
    render(<ConversationView sessionName={SESSION} />)
    act(() => {
      useStreamStore.getState().setRelayStatus(SESSION, true)
      useStreamStore.getState().setStreaming(SESSION, true)
    })
    expect(screen.getByTestId('thinking-indicator')).toBeInTheDocument()
  })

  it('transitions from HandoffButton to conversation when relay connects', () => {
    render(<ConversationView sessionName={SESSION} />)
    // Initially: no relay → show Handoff
    expect(screen.getByText('Handoff')).toBeInTheDocument()
    // Relay connects → show conversation
    act(() => {
      useStreamStore.getState().setRelayStatus(SESSION, true)
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
      relayStatus: { [SESSION]: true },
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
      relayStatus: { [SESSION]: true },
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
      relayStatus: { [SESSION]: true },
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
      relayStatus: { [SESSION]: true },
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
      relayStatus: { [SESSION]: true },
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
      relayStatus: { [SESSION]: true },
    })

    render(<ConversationView sessionName={SESSION} />)

    const block = screen.getByTestId('tool-result-block')
    expect(block.className).toContain('border-[#302a2a]')
  })
})
