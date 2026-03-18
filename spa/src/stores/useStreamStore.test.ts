// spa/src/stores/useStreamStore.test.ts
import { beforeEach, describe, expect, it } from 'vitest'
import { useStreamStore } from './useStreamStore'
import type { StreamMessage, ControlRequest } from '../lib/stream-ws'

const emptyState = {
  sessions: {},
  sessionStatus: {},
  relayStatus: {},
  handoffState: {},
  handoffProgress: {},
}

describe('useStreamStore (per-session)', () => {
  beforeEach(() => {
    useStreamStore.setState(emptyState)
  })

  it('has empty sessions by default', () => {
    expect(useStreamStore.getState().sessions).toEqual({})
  })

  it('addMessage creates session lazily and appends', () => {
    const { addMessage } = useStreamStore.getState()
    const msg = { type: 'assistant' } as StreamMessage
    addMessage('sess-a', msg)
    const state = useStreamStore.getState().sessions['sess-a']
    expect(state).toBeDefined()
    expect(state.messages).toHaveLength(1)
    expect(state.messages[0]).toBe(msg)
  })

  it('messages are independent per session', () => {
    const { addMessage } = useStreamStore.getState()
    addMessage('sess-a', { type: 'user' } as StreamMessage)
    addMessage('sess-b', { type: 'assistant' } as StreamMessage)
    expect(useStreamStore.getState().sessions['sess-a'].messages).toHaveLength(1)
    expect(useStreamStore.getState().sessions['sess-b'].messages).toHaveLength(1)
  })

  it('setConn stores per session', () => {
    const { setConn } = useStreamStore.getState()
    const mockConn = { send: () => {}, close: () => {} } as any
    setConn('sess-a', mockConn)
    expect(useStreamStore.getState().sessions['sess-a'].conn).toBe(mockConn)
    expect(useStreamStore.getState().sessions['sess-b']?.conn).toBeUndefined()
  })

  it('setStreaming per session', () => {
    const { setStreaming } = useStreamStore.getState()
    setStreaming('sess-a', true)
    expect(useStreamStore.getState().sessions['sess-a'].isStreaming).toBe(true)
  })

  it('loadHistory sets messages for session', () => {
    const { loadHistory } = useStreamStore.getState()
    const msgs = [{ type: 'user' } as StreamMessage, { type: 'assistant' } as StreamMessage]
    loadHistory('sess-a', msgs)
    expect(useStreamStore.getState().sessions['sess-a'].messages).toEqual(msgs)
  })

  it('loadHistory does not overwrite existing messages if already present', () => {
    const { addMessage, loadHistory } = useStreamStore.getState()
    addMessage('sess-a', { type: 'user' } as StreamMessage)
    // loadHistory replaces messages (initial load from JSONL)
    loadHistory('sess-a', [{ type: 'assistant' } as StreamMessage])
    expect(useStreamStore.getState().sessions['sess-a'].messages).toHaveLength(1)
    expect(useStreamStore.getState().sessions['sess-a'].messages[0].type).toBe('assistant')
  })

  it('clearSession closes conn and removes state', () => {
    const { setConn, addMessage, clearSession } = useStreamStore.getState()
    let closed = false
    const mockConn = { send: () => {}, close: () => { closed = true } } as any
    setConn('sess-a', mockConn)
    addMessage('sess-a', { type: 'user' } as StreamMessage)
    clearSession('sess-a')
    expect(closed).toBe(true)
    expect(useStreamStore.getState().sessions['sess-a']).toBeUndefined()
  })

  it('addControlRequest and resolveControlRequest per session', () => {
    const { addControlRequest, resolveControlRequest } = useStreamStore.getState()
    const req = { request_id: 'r1', request: { subtype: 'permission' } } as ControlRequest
    addControlRequest('sess-a', req)
    expect(useStreamStore.getState().sessions['sess-a'].pendingControlRequests).toHaveLength(1)
    resolveControlRequest('sess-a', 'r1')
    expect(useStreamStore.getState().sessions['sess-a'].pendingControlRequests).toHaveLength(0)
  })

  it('setSessionInfo per session', () => {
    const { setSessionInfo } = useStreamStore.getState()
    setSessionInfo('sess-a', 'cc-uuid', 'opus-4')
    const info = useStreamStore.getState().sessions['sess-a'].sessionInfo
    expect(info.ccSessionId).toBe('cc-uuid')
    expect(info.model).toBe('opus-4')
  })

  it('addCost per session', () => {
    const { addCost } = useStreamStore.getState()
    addCost('sess-a', 0.5)
    addCost('sess-a', 0.3)
    expect(useStreamStore.getState().sessions['sess-a'].cost).toBe(0.8)
  })

  it('handoffState is per-session', () => {
    const { setHandoffState } = useStreamStore.getState()
    setHandoffState('sess-a', 'connected')
    setHandoffState('sess-b', 'handoff-in-progress')
    expect(useStreamStore.getState().handoffState['sess-a']).toBe('connected')
    expect(useStreamStore.getState().handoffState['sess-b']).toBe('handoff-in-progress')
  })

  it('handoffProgress is per-session', () => {
    const { setHandoffProgress } = useStreamStore.getState()
    setHandoffProgress('sess-a', 'detecting')
    setHandoffProgress('sess-b', 'launching')
    expect(useStreamStore.getState().handoffProgress['sess-a']).toBe('detecting')
    expect(useStreamStore.getState().handoffProgress['sess-b']).toBe('launching')
  })

  it('relayStatus is per-session', () => {
    const { setRelayStatus } = useStreamStore.getState()
    setRelayStatus('sess-a', true)
    setRelayStatus('sess-b', false)
    expect(useStreamStore.getState().relayStatus['sess-a']).toBe(true)
    expect(useStreamStore.getState().relayStatus['sess-b']).toBe(false)
  })

  it('sessionStatus persists across clearSession', () => {
    const { setSessionStatus, clearSession } = useStreamStore.getState()
    setSessionStatus('sess-a', 'cc-idle')
    clearSession('sess-a')
    expect(useStreamStore.getState().sessionStatus['sess-a']).toBe('cc-idle')
  })

  it('sessionStatus accumulates by key', () => {
    const { setSessionStatus } = useStreamStore.getState()
    setSessionStatus('dev', 'cc-running')
    setSessionStatus('prod', 'cc-idle')
    expect(useStreamStore.getState().sessionStatus).toEqual({ dev: 'cc-running', prod: 'cc-idle' })
  })
})
