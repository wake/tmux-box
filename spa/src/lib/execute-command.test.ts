// spa/src/lib/execute-command.test.ts — the shared send-keys caller behind
// Quick Commands and the slot executor.
//
// `POST /api/sessions/{code}/send-keys` grew an optional
// `expected_tmux_instance` precondition for the rebuild resume (spec §4.6.2).
// This path states no expectation and must keep sending exactly what it always
// sent: a request carrying an expectation the daemon cannot satisfy is refused
// with 409, which for Quick Commands would be a regression, not a safeguard.
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { executeCommand } from './execute-command'
import { useHostStore } from '../stores/useHostStore'

const HOST_ID = 'h1'

beforeEach(() => {
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '10.0.0.9', port: 7860, token: null, order: 0 } },
    hostOrder: [HOST_ID],
    activeHostId: HOST_ID,
  })
  vi.unstubAllGlobals()
})

describe('executeCommand', () => {
  it('sends keys only, stating no generation expectation', async () => {
    const fetchMock = vi.fn(async () => new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)

    await executeCommand(HOST_ID, 'abc123', 'echo hi')

    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('http://10.0.0.9:7860/api/sessions/abc123/send-keys')
    expect(JSON.parse(String(init.body))).toEqual({ keys: 'echo hi\n' })
  })

  it('throws on a non-ok response', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('nope', { status: 500 })))
    await expect(executeCommand(HOST_ID, 'abc123', 'echo hi')).rejects.toThrow('500')
  })
})
