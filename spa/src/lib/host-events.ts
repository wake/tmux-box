// spa/src/lib/host-events.ts

export interface HostEvent {
  type:
    | 'handoff'
    | 'relay'
    | 'hook'
    | 'sessions'
    | 'tmux'
    | 'agent.status'
    | 'agent.status.cleared'
    | 'agent.path_hint'
    | 'backup:done'
  session: string
  value: string
}

export interface EventConnection {
  close: () => void
  reconnect: () => void
  reconnectWithTicket: (ticket?: string) => void
}

export function connectHostEvents(
  url: string,
  onEvent: (event: HostEvent) => void,
  onClose?: () => void,
  onOpen?: () => void,
  getTicket?: () => Promise<string>,
  autoReconnect = true,
  lazy = false,
): EventConnection {
  let ws: WebSocket
  let retryMs = 1000
  let closed = false
  let connecting = false
  let pendingTicket: string | undefined
  // Connection epoch (spec §4.6). `reconnect` / `reconnectWithTicket` reuse this
  // same connection object and the same `onEvent` closure, so the only place a
  // per-socket identity exists is here, where each socket's handlers are
  // created. Frames from a superseded socket — and connect attempts whose
  // ticket resolved after a newer attempt started — are dropped.
  let socketEpoch = 0

  async function connect() {
    if (connecting) return
    connecting = true
    // Set when this attempt is abandoned mid-flight: a newer attempt claimed
    // the epoch while we were awaiting the ticket, but was itself turned away
    // by the `connecting` guard above, so this attempt has to hand over.
    let superseded = false
    try {
      let wsUrl = url
      // Claim the epoch BEFORE the await so a slower ticket cannot resurrect a
      // superseded attempt.
      const myEpoch = ++socketEpoch
      const ticket = pendingTicket ?? (getTicket ? await getTicket().catch(() => null) : null)
      if (closed || myEpoch !== socketEpoch) {
        // Leave `pendingTicket` alone — it belongs to the attempt that
        // superseded us and must survive into the hand-over below.
        superseded = !closed
        return
      }
      pendingTicket = undefined

      if (ticket) {
        const u = new URL(wsUrl)
        u.searchParams.set('ticket', ticket)
        wsUrl = u.toString()
      } else if (getTicket) {
        if (!closed) onClose?.()
        return
      }

      ws = new WebSocket(wsUrl)
      ws.onopen = () => { retryMs = 1000; onOpen?.() }
      ws.onmessage = (e) => {
        if (myEpoch !== socketEpoch) return // superseded socket's queued frames
        try {
          const event = JSON.parse(e.data) as HostEvent
          onEvent(event)
        } catch { /* ignore parse errors */ }
      }
      ws.onerror = () => {}
      ws.onclose = () => {
        if (closed) return
        onClose?.()
        if (autoReconnect) {
          setTimeout(() => { if (!closed) connect() }, retryMs)
          retryMs = Math.min(retryMs * 2, 30000)
        }
      }
    } finally {
      connecting = false
      // The hand-over: the attempt that superseded us bounced off the
      // `connecting` guard, so nothing else will open the socket it asked for.
      if (superseded) connect()
    }
  }

  /**
   * Retire the live socket and invalidate the in-flight attempt, if any. The
   * epoch bump is what makes a `connecting`-guarded call still count as a
   * supersede rather than silently doing nothing.
   */
  function supersede() {
    socketEpoch++
    retryMs = 1000
    if (ws) { ws.onclose = null; ws.close() }  // 清除 onclose 防止 double-trigger
  }

  if (!lazy) connect()
  return {
    close: () => { closed = true; ws?.close() },
    reconnect: () => {
      if (!closed) {
        supersede()
        connect()
      }
    },
    reconnectWithTicket: (ticket) => {
      if (!closed) {
        pendingTicket = ticket
        supersede()
        connect()
      }
    },
  }
}
