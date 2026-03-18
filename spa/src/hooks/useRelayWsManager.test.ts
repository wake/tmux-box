// spa/src/hooks/useRelayWsManager.test.ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useStreamStore } from '../stores/useStreamStore'

const emptyState = {
  sessions: {},
  sessionStatus: {},
  relayStatus: {},
  handoffProgress: {},
}

describe('useRelayWsManager store integration', () => {
  beforeEach(() => {
    useStreamStore.setState(emptyState)
  })

  it('setRelayStatus triggers store update', () => {
    useStreamStore.getState().setRelayStatus('test', true)
    expect(useStreamStore.getState().relayStatus['test']).toBe(true)
  })

  it('setConn stores connection for session', () => {
    const mockConn = { send: vi.fn(), close: vi.fn() } as any
    useStreamStore.getState().setConn('test', mockConn)
    expect(useStreamStore.getState().sessions['test'].conn).toBe(mockConn)
  })

  it('clearing relay status and conn works together', () => {
    const mockConn = { send: vi.fn(), close: vi.fn() } as any
    useStreamStore.getState().setConn('test', mockConn)
    useStreamStore.getState().setRelayStatus('test', true)

    // Simulate disconnect
    useStreamStore.getState().setRelayStatus('test', false)
    useStreamStore.getState().sessions['test']?.conn?.close()
    useStreamStore.getState().setConn('test', null)

    expect(useStreamStore.getState().relayStatus['test']).toBe(false)
    expect(useStreamStore.getState().sessions['test'].conn).toBeNull()
    expect(mockConn.close).toHaveBeenCalled()
  })

  it('subscribeWithSelector works for relayStatus changes', () => {
    const changes: Record<string, boolean>[] = []
    const unsub = useStreamStore.subscribe(
      (s) => s.relayStatus,
      (relayStatus) => { changes.push({ ...relayStatus }) },
    )

    useStreamStore.getState().setRelayStatus('sess-a', true)
    useStreamStore.getState().setRelayStatus('sess-b', false)

    expect(changes).toHaveLength(2)
    expect(changes[0]).toEqual({ 'sess-a': true })
    expect(changes[1]).toEqual({ 'sess-a': true, 'sess-b': false })

    unsub()
  })
})
