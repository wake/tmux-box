// spa/src/lib/session-events.ts

export interface SessionEvent {
  type: 'status' | 'handoff'
  session: string
  value: string
}

export interface EventConnection {
  close: () => void
}

export function connectSessionEvents(
  url: string,
  onEvent: (event: SessionEvent) => void,
  onClose?: () => void,
): EventConnection {
  let ws: WebSocket
  let retryMs = 1000
  let closed = false

  function connect() {
    ws = new WebSocket(url)
    ws.onopen = () => { retryMs = 1000 } // reset backoff on success
    ws.onmessage = (e) => {
      try {
        const event = JSON.parse(e.data) as SessionEvent
        onEvent(event)
      } catch { /* ignore parse errors */ }
    }
    ws.onclose = () => {
      if (closed) return
      onClose?.()
      // Reconnect with exponential backoff (max 30s)
      setTimeout(() => {
        if (!closed) connect()
      }, retryMs)
      retryMs = Math.min(retryMs * 2, 30000)
    }
  }

  connect()
  return { close: () => { closed = true; ws.close() } }
}
