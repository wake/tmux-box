// spa/src/lib/host-events.test.ts
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { connectHostEvents } from './host-events'

class MockWebSocket {
  static CONNECTING = 0
  static OPEN = 1
  static CLOSED = 3
  readyState = MockWebSocket.CONNECTING
  binaryType = ''
  url: string
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onmessage: ((e: { data: unknown }) => void) | null = null
  onerror: (() => void) | null = null
  send = vi.fn()
  close = vi.fn(() => { this.readyState = MockWebSocket.CLOSED; this.onclose?.() })
  simulateOpen() { this.readyState = MockWebSocket.OPEN; this.onopen?.() }
  constructor(url: string) { this.url = url; wsInstances.push(this) }
}

let wsInstances: MockWebSocket[] = []

beforeEach(() => {
  wsInstances = []
  vi.stubGlobal('WebSocket', MockWebSocket)
})
afterEach(() => { vi.unstubAllGlobals() })

describe('connectHostEvents', () => {
  it('lazy mode does not connect immediately', () => {
    connectHostEvents('ws://test/ws/host-events', vi.fn(), undefined, undefined, undefined, false, true)
    expect(wsInstances).toHaveLength(0)
  })

  it('reconnectWithTicket creates WS with ticket in URL', async () => {
    const conn = connectHostEvents('ws://test/ws/host-events', vi.fn(), undefined, undefined, undefined, false, true)
    conn.reconnectWithTicket('tk_pre')
    await vi.dynamicImportSettled?.() // flush microtasks
    await new Promise((r) => setTimeout(r, 0))
    expect(wsInstances).toHaveLength(1)
    expect(wsInstances[0].url).toContain('ticket=tk_pre')
  })

  it('pendingTicket is consumed once, second connect falls back to getTicket', async () => {
    const getTicket = vi.fn().mockResolvedValue('tk_callback')
    const conn = connectHostEvents('ws://test/ws/host-events', vi.fn(), undefined, undefined, getTicket, false, true)
    conn.reconnectWithTicket('tk_once')
    await new Promise((r) => setTimeout(r, 0))
    expect(wsInstances[0].url).toContain('ticket=tk_once')
    // Simulate WS close → reconnect without pendingTicket
    wsInstances[0].close()
    conn.reconnect()
    await new Promise((r) => setTimeout(r, 0))
    expect(getTicket).toHaveBeenCalled()
    expect(wsInstances[1].url).toContain('ticket=tk_callback')
  })

  it('getTicket failure with no pendingTicket calls onClose', async () => {
    const onClose = vi.fn()
    const getTicket = vi.fn().mockRejectedValue(new Error('401'))
    const conn = connectHostEvents('ws://test/ws/host-events', vi.fn(), onClose, undefined, getTicket, false, true)
    conn.reconnect()
    await new Promise((r) => setTimeout(r, 0))
    expect(onClose).toHaveBeenCalled()
    expect(wsInstances).toHaveLength(0) // no WS created
  })

  // The `connecting` flag still serializes connects, but it no longer *drops*
  // the newer one: under the epoch contract (spec §4.6) the in-flight attempt
  // is superseded and hands over, so the second reconnect is honoured with its
  // own ticket instead of silently inheriting the stale in-flight one.
  it('serializes overlapping connects — one socket, and the newest attempt wins', async () => {
    const getTicket = vi.fn().mockImplementation(() => new Promise((r) => setTimeout(() => r('tk'), 100)))
    const conn = connectHostEvents('ws://test/ws/host-events', vi.fn(), undefined, undefined, getTicket, false, true)
    conn.reconnect()
    conn.reconnect() // second call while first is awaiting getTicket
    await new Promise((r) => setTimeout(r, 150))
    expect(wsInstances).toHaveLength(0) // the superseded attempt opened nothing
    await new Promise((r) => setTimeout(r, 150))
    expect(wsInstances).toHaveLength(1) // the hand-over opened exactly one
  })
})
