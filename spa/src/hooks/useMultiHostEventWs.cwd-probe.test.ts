// spa/src/hooks/useMultiHostEventWs.cwd-probe.test.ts — the first of the two
// cwd-probe triggers (spec §4.4): a reconciled `sessions` payload sweeps every
// pane on that host whose record still has no cwd.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { probeMissingCwds } from '../lib/rebuild/cwd-probe'

vi.mock('../lib/host-connection', () => ({
  checkHealth: vi.fn(async () => ({ daemon: 'connected', latency: 3, ticket: 'tk' })),
}))
vi.mock('../lib/rebuild/cwd-probe', () => ({
  probeMissingCwds: vi.fn(),
  probeSessionCwd: vi.fn(),
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

beforeEach(() => {
  sockets = []
  vi.mocked(probeMissingCwds).mockClear()
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

describe('useMultiHostEventWs cwd probe', () => {
  it('sweeps the host for missing cwds after a reconciled sessions payload', async () => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    expect(probeMissingCwds).not.toHaveBeenCalled()

    act(() => {
      sockets[0].emit(JSON.stringify({
        type: 'sessions', session: '',
        value: JSON.stringify([{ code: 'abc123', name: 'dev', tmux_instance: '222:2000' }]),
      }))
    })

    expect(probeMissingCwds).toHaveBeenCalledWith(HOST)
    view.unmount()
  })

  it('does not sweep on a payload that could not be parsed', async () => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))

    act(() => { sockets[0].emit(JSON.stringify({ type: 'sessions', session: '', value: 'not-json' })) })

    expect(probeMissingCwds).not.toHaveBeenCalled()
    view.unmount()
  })
})
