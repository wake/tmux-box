// spa/src/hooks/useMultiHostEventWs.gate.test.ts — the gate's open/close wiring
// (spec §4.6). The gate closes whenever a host-events connection is (re)started
// or drops, and reopens only when that connection's own `sessions` payload has
// been reconciled.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'

vi.mock('../lib/host-connection', () => ({
  checkHealth: vi.fn(async () => ({ daemon: 'connected', latency: 3, ticket: 'tk' })),
}))

const { useMultiHostEventWs } = await import('./useMultiHostEventWs')

const HOST = 'h1'

class FakeSocket {
  static OPEN = 1
  readyState = 0
  binaryType = ''
  url: string
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onmessage: ((e: { data: unknown }) => void) | null = null
  onerror: (() => void) | null = null
  send = vi.fn()
  close = vi.fn(() => { this.readyState = 3 })
  constructor(url: string) { this.url = url; sockets.push(this) }
  emit(data: string) { this.onmessage?.({ data }) }
}

let sockets: FakeSocket[] = []

const attachReady = () => useHostStore.getState().runtime[HOST]?.attachReady

beforeEach(() => {
  sockets = []
  vi.stubGlobal('WebSocket', FakeSocket)
  useHostStore.setState({
    hosts: { [HOST]: { id: HOST, name: 'Host', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST],
    runtime: {},
    activeHostId: HOST,
  })
  useSessionStore.setState({
    fetchHost: vi.fn(async () => {}),
    replaceHost: vi.fn(),
  } as never)
})

afterEach(() => {
  vi.unstubAllGlobals()
  useHostStore.getState().reset()
})

describe('useMultiHostEventWs attach gate', () => {
  it('keeps the gate closed until this connection delivers a sessions payload', async () => {
    const view = renderHook(() => useMultiHostEventWs())

    expect(attachReady()).toBe(false)
    await waitFor(() => expect(sockets).toHaveLength(1))
    expect(attachReady()).toBe(false)

    act(() => { sockets[0].onopen?.() })
    expect(attachReady()).toBe(false) // an open socket is not yet a fresh list

    act(() => { sockets[0].emit(JSON.stringify({ type: 'sessions', session: '', value: '[]' })) })
    expect(attachReady()).toBe(true)

    view.unmount()
  })

  it('closes the gate again as soon as the socket drops', async () => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    act(() => { sockets[0].emit(JSON.stringify({ type: 'sessions', session: '', value: '[]' })) })
    expect(attachReady()).toBe(true)

    act(() => { sockets[0].onclose?.() })
    expect(attachReady()).toBe(false)

    view.unmount()
  })

  it('leaves the gate closed when the payload cannot be parsed', async () => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))

    act(() => { sockets[0].emit(JSON.stringify({ type: 'sessions', session: '', value: 'not-json' })) })
    expect(attachReady()).toBe(false)

    view.unmount()
  })
})
