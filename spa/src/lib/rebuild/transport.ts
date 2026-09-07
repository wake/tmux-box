// spa/src/lib/rebuild/transport.ts — the rebuild operation's host-pinned
// transport (spec §4.8).
//
// Why this file exists at all: `hostFetch` resolves its base URL through
// `useHostStore.getDaemonBase`, which silently falls back to the ACTIVE host
// when `hostId` is unknown (`useHostStore.ts:149-156`). A host removed while a
// rebuild is in flight would therefore create the session — and fire the resume
// command — on a different machine. `createSession` (`host-api.ts`) and
// `executeCommand` (`execute-command.ts`) both go through `hostFetch`, so the
// engine cannot reuse either: it needs the requests themselves, pinned.
import { useHostStore } from '../../stores/useHostStore'
import { HostApiError } from '../host-api'
import type { Session } from '../host-api'

/** The address an operation is pinned to — what "the same host" means here. */
export interface HostIdentity {
  ip: string
  port: number
  token: string | null
}

export interface PinnedTransport {
  hostId: string
  /** The configuration this transport is pinned to, for a later operation to re-pin against. */
  identity: HostIdentity
  /** Throws if the host's ip/port/token changed (or vanished) since the pin. */
  assertUnchanged(): void
  createSession(name: string, cwd: string, mode: string): Promise<Session>
  sendKeys(sessionCode: string, command: string): Promise<void>
}

/** Two host configurations are the same machine, credentials included. */
function sameIdentity(a: HostIdentity, b: HostIdentity): boolean {
  return a.ip === b.ip && a.port === b.port && a.token === b.token
}

/**
 * Pin a host's address once, for the lifetime of one rebuild operation.
 *
 * Throws when the host does not exist — deliberately, because that is exactly
 * the case `getDaemonBase` would answer with the active host's address.
 *
 * `expected` is the identity an EARLIER stage of the same operation pinned.
 * A retry re-pins from the host id, so without it the retry would happily
 * resolve whatever address that id points at now and send a resume command
 * recorded against the old machine to a new one.
 */
export function pinHost(hostId: string, expected?: HostIdentity): PinnedTransport {
  const host = useHostStore.getState().hosts[hostId]
  if (!host) throw new Error(`host ${hostId} is not configured`)
  const pinned: HostIdentity = { ip: host.ip, port: host.port, token: host.token ?? null }
  if (expected && !sameIdentity(pinned, expected)) {
    throw new Error(`host ${hostId} now points at ${pinned.ip}:${pinned.port}, not the ${expected.ip}:${expected.port} this rebuild ran against`)
  }
  const base = `http://${pinned.ip}:${pinned.port}`

  function assertUnchanged() {
    const now = useHostStore.getState().hosts[hostId]
    if (!now || now.ip !== pinned.ip || now.port !== pinned.port || (now.token ?? null) !== pinned.token) {
      throw new Error(`host ${hostId} changed during the operation`)
    }
  }

  const request = async (path: string, body: unknown): Promise<Response> => {
    // Re-verified immediately before every mutation, not just at pin time.
    assertUnchanged()
    const headers: Record<string, string> = { 'Content-Type': 'application/json' }
    // Header name and format copied verbatim from `useHostStore.getAuthHeaders`
    // (`useHostStore.ts:167-171`), which also omits the header for a falsy token.
    if (pinned.token) headers.Authorization = `Bearer ${pinned.token}`
    return fetch(`${base}${path}`, { method: 'POST', headers, body: JSON.stringify(body) })
  }

  return {
    hostId,
    identity: pinned,
    assertUnchanged,
    async createSession(name, cwd, mode) {
      const res = await request('/api/sessions', { name, cwd, mode })
      if (!res.ok) throw new HostApiError(res.status, res.statusText)
      return res.json()
    },
    async sendKeys(sessionCode, command) {
      const res = await request(`/api/sessions/${sessionCode}/send-keys`, { keys: command + '\n' })
      if (!res.ok) throw new HostApiError(res.status, res.statusText)
    },
  }
}
