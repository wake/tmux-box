// spa/src/stores/useStreamStore.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { useStreamStore } from './useStreamStore'

beforeEach(() => {
  useStreamStore.setState({
    messages: [],
    pendingControlRequests: [],
    isStreaming: false,
    sessionId: null,
    model: null,
    cost: 0,
    handoffState: 'idle',
  })
})

describe('useStreamStore', () => {
  it('adds assistant message', () => {
    const { addMessage } = useStreamStore.getState()
    addMessage({
      type: 'assistant',
      message: {
        role: 'assistant',
        content: [{ type: 'text', text: 'hi' }],
        stop_reason: 'end_turn',
      },
    })
    expect(useStreamStore.getState().messages).toHaveLength(1)
  })

  it('adds control request', () => {
    const { addControlRequest } = useStreamStore.getState()
    addControlRequest({
      type: 'control_request',
      request_id: 'req-1',
      request: { subtype: 'can_use_tool', tool_name: 'Bash', input: { command: 'ls' } },
    })
    expect(useStreamStore.getState().pendingControlRequests).toHaveLength(1)
  })

  it('resolves control request', () => {
    const { addControlRequest, resolveControlRequest } = useStreamStore.getState()
    addControlRequest({
      type: 'control_request',
      request_id: 'req-1',
      request: { subtype: 'can_use_tool', tool_name: 'Bash', input: {} },
    })
    resolveControlRequest('req-1')
    expect(useStreamStore.getState().pendingControlRequests).toHaveLength(0)
  })

  it('tracks streaming state', () => {
    const { setStreaming } = useStreamStore.getState()
    setStreaming(true)
    expect(useStreamStore.getState().isStreaming).toBe(true)
  })

  it('clears messages', () => {
    const { addMessage, clear } = useStreamStore.getState()
    addMessage({
      type: 'assistant',
      message: { role: 'assistant', content: [{ type: 'text', text: 'x' }], stop_reason: null },
    })
    clear()
    expect(useStreamStore.getState().messages).toHaveLength(0)
  })

  it('tracks handoff state', () => {
    const { setHandoffState } = useStreamStore.getState()
    expect(useStreamStore.getState().handoffState).toBe('idle')
    setHandoffState('handoff-in-progress')
    expect(useStreamStore.getState().handoffState).toBe('handoff-in-progress')
    setHandoffState('connected')
    expect(useStreamStore.getState().handoffState).toBe('connected')
  })

  it('clear resets handoff state to idle', () => {
    const { setHandoffState, clear } = useStreamStore.getState()
    setHandoffState('connected')
    clear()
    expect(useStreamStore.getState().handoffState).toBe('idle')
  })
})
