// spa/src/lib/host-events.epoch.test.ts — the connection epoch lives inside the
// transport (spec §4.6). `reconnect` / `reconnectWithTicket` reuse the same
// connection object and the same `onEvent` closure, so a consumer-side epoch
// would reject the *new* socket's frames too; the filter has to sit where each
// socket's handlers are created.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { connectHostEvents } from './host-events'

class FakeSocket {
  static CONNECTING = 0
  static OPEN = 1
  static CLOSED = 3
  readyState = FakeSocket.CONNECTING
  url: string
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onmessage: ((e: { data: unknown }) => void) | null = null
  onerror: (() => void) | null = null
  send = vi.fn()
  close = vi.fn(() => { this.readyState = FakeSocket.CLOSED; this.onclose?.() })
  constructor(url: string) { this.url = url }
  /** Deliver a frame the way a real socket would. */
  emit(data: string) { this.onmessage?.({ data }) }
}

let sockets: FakeSocket[] = []

const sessionsFrame = JSON.stringify({ type: 'sessions', session: '', value: '[]' })

beforeEach(() => {
  sockets = []
  vi.stubGlobal('WebSocket', class extends FakeSocket {
    constructor(url: string) { super(url); sockets.push(this) }
  })
})

afterEach(() => { vi.unstubAllGlobals() })

describe('connectHostEvents socket epoch', () => {
  it('drops frames from a socket that reconnect superseded', () => {
    const onEvent = vi.fn()
    const conn = connectHostEvents('ws://h/events', onEvent)
    expect(sockets).toHaveLength(1)

    conn.reconnect() // second socket supersedes the first
    expect(sockets).toHaveLength(2)

    sockets[0].emit(sessionsFrame)
    expect(onEvent).not.toHaveBeenCalled()

    sockets[1].emit(sessionsFrame)
    expect(onEvent).toHaveBeenCalledTimes(1)

    conn.close()
  })

  it('still delivers frames on a plain single connection', () => {
    const onEvent = vi.fn()
    const conn = connectHostEvents('ws://h/events', onEvent)
    sockets[0].emit(sessionsFrame)
    expect(onEvent).toHaveBeenCalledTimes(1)
    conn.close()
  })

  it('abandons a connect whose ticket resolved after a newer connect started', async () => {
    let releaseTicket!: (t: string) => void
    const getTicket = () => new Promise<string>((r) => { releaseTicket = r })
    const conn = connectHostEvents('ws://h/events', vi.fn(), undefined, undefined, getTicket)

    conn.reconnectWithTicket('fresh') // supersedes the pending first attempt
    releaseTicket('stale')
    await new Promise((r) => setTimeout(r, 0))

    expect(sockets.filter((s) => s.url.includes('ticket=stale'))).toHaveLength(0)
    expect(sockets.filter((s) => s.url.includes('ticket=fresh'))).toHaveLength(1)

    conn.close()
  })
})
