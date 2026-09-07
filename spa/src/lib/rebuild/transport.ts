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
  /**
   * `expectedTmuxInstance` is the tmux generation the caller believes the code
   * belongs to (spec §4.6.2). The daemon refuses with 409 and sends nothing
   * when it does not hold — the only authoritative check there is, because
   * only the daemon knows the generation at the moment it acts.
   *
   * Required and non-empty on THIS transport, which exists only to carry a
   * rebuild. "No expectation" is a legitimate request — Quick Commands has no
   * generation to state — but it is not one a rebuild may make, so it is not
   * representable here: an empty value throws rather than sending.
   */
  sendKeys(sessionCode: string, command: string, expectedTmuxInstance: string): Promise<void>
}

/**
 * The daemon refused the keystroke: the code no longer belongs to the
 * generation the caller named. Not a transient failure — re-sending the same
 * request can only be refused again — so it is surfaced as a reason the panel
 * shows, never retried automatically.
 *
 * Deliberately not a `HostApiError` subclass: `extends` is evaluated when this
 * module loads, which would make every suite that partially mocks `host-api`
 * fail at import time rather than at call time.
 */
export class GenerationConflictError extends Error {
  readonly status = 409
  constructor(sessionCode: string, expected: string) {
    super(`session ${sessionCode} no longer belongs to tmux generation ${expected}`)
    this.name = 'GenerationConflictError'
  }
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
    async sendKeys(sessionCode, command, expectedTmuxInstance) {
      if (!expectedTmuxInstance) {
        throw new Error(`refusing to send keys to ${sessionCode} without a tmux generation to assert`)
      }
      const res = await request(`/api/sessions/${sessionCode}/send-keys`, {
        keys: command + '\n',
        expected_tmux_instance: expectedTmuxInstance,
      })
      if (res.status === 409) {
        throw new GenerationConflictError(sessionCode, expectedTmuxInstance)
      }
      if (!res.ok) throw new HostApiError(res.status, res.statusText)
    },
  }
}
