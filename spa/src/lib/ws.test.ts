import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { connectTerminal } from './ws'

// Mock WebSocket
class MockWebSocket {
  static CONNECTING = 0
  static OPEN = 1
  static CLOSING = 2
  static CLOSED = 3

  readyState = MockWebSocket.CONNECTING
  binaryType = ''
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onmessage: ((e: { data: unknown }) => void) | null = null
  onerror: (() => void) | null = null

  send = vi.fn()
  close = vi.fn(() => {
    this.readyState = MockWebSocket.CLOSED
    this.onclose?.()
  })

  // Test helpers
  simulateOpen() {
    this.readyState = MockWebSocket.OPEN
    this.onopen?.()
  }
  simulateClose() {
    this.readyState = MockWebSocket.CLOSED
    this.onclose?.()
  }
}

let wsInstances: MockWebSocket[] = []

beforeEach(() => {
  wsInstances = []
  vi.stubGlobal('WebSocket', class extends MockWebSocket {
    constructor() {
      super()
      wsInstances.push(this)
    }
  })
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('connectTerminal auto-reconnect', () => {
  it('calls onClose when WS disconnects', () => {
    const onClose = vi.fn()
    connectTerminal('ws://test', vi.fn(), onClose)
    wsInstances[0].simulateOpen()
    wsInstances[0].simulateClose()
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('creates a new WebSocket after disconnect with exponential backoff', () => {
    connectTerminal('ws://test', vi.fn(), vi.fn())
    wsInstances[0].simulateOpen()
    wsInstances[0].simulateClose()
    expect(wsInstances).toHaveLength(1) // not yet reconnected

    vi.advanceTimersByTime(1000) // first retry at 1s
    expect(wsInstances).toHaveLength(2) // reconnected
  })

  it('doubles backoff on repeated failures', () => {
    connectTerminal('ws://test', vi.fn(), vi.fn())
    wsInstances[0].simulateOpen()
    wsInstances[0].simulateClose()

    // 1st retry at 1s
    vi.advanceTimersByTime(1000)
    expect(wsInstances).toHaveLength(2)
    wsInstances[1].simulateClose() // fail again

    // 2nd retry at 2s
    vi.advanceTimersByTime(1999)
    expect(wsInstances).toHaveLength(2) // not yet
    vi.advanceTimersByTime(1)
    expect(wsInstances).toHaveLength(3)
  })

  it('caps backoff at 30s', () => {
    connectTerminal('ws://test', vi.fn(), vi.fn())
    wsInstances[0].simulateOpen()

    // Fail many times to exceed 30s cap
    for (let i = 0; i < 10; i++) {
      wsInstances[wsInstances.length - 1].simulateClose()
      vi.advanceTimersByTime(30000)
    }
    // Should still be reconnecting, not exceeding 30s
    const lastIdx = wsInstances.length
    wsInstances[wsInstances.length - 1].simulateClose()
    vi.advanceTimersByTime(30000)
    expect(wsInstances.length).toBe(lastIdx + 1)
  })

  it('resets backoff on successful reconnect', () => {
    connectTerminal('ws://test', vi.fn(), vi.fn())
    wsInstances[0].simulateOpen()
    wsInstances[0].simulateClose()

    // 1st retry at 1s, fails
    vi.advanceTimersByTime(1000)
    wsInstances[1].simulateClose()

    // 2nd retry at 2s, succeeds
    vi.advanceTimersByTime(2000)
    wsInstances[2].simulateOpen() // success — backoff should reset
    wsInstances[2].simulateClose()

    // Next retry should be at 1s again (reset)
    vi.advanceTimersByTime(1000)
    expect(wsInstances).toHaveLength(4)
  })

  it('calls onOpen on reconnect', () => {
    const onOpen = vi.fn()
    connectTerminal('ws://test', vi.fn(), vi.fn(), onOpen)
    wsInstances[0].simulateOpen()
    expect(onOpen).toHaveBeenCalledTimes(1)

    wsInstances[0].simulateClose()
    vi.advanceTimersByTime(1000)
    wsInstances[1].simulateOpen()
    expect(onOpen).toHaveBeenCalledTimes(2)
  })

  it('stops reconnecting and does not call onClose after manual close()', () => {
    const onClose = vi.fn()
    const conn = connectTerminal('ws://test', vi.fn(), onClose)
    wsInstances[0].simulateOpen()
    conn.close() // manual close

    vi.advanceTimersByTime(5000)
    expect(wsInstances).toHaveLength(1) // no reconnect attempt
    expect(onClose).not.toHaveBeenCalled() // onClose not called on manual close
  })

  it('retries even if WS never opened (initial connect failure)', () => {
    connectTerminal('ws://test', vi.fn(), vi.fn())
    wsInstances[0].simulateClose() // never opened

    vi.advanceTimersByTime(1000)
    expect(wsInstances).toHaveLength(2) // still retries initial connect
  })
})
