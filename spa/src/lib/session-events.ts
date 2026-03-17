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
  const ws = new WebSocket(url)
  ws.onmessage = (e) => {
    try {
      const event = JSON.parse(e.data) as SessionEvent
      onEvent(event)
    } catch {
      /* ignore parse errors */
    }
  }
  ws.onclose = () => onClose?.()
  return { close: () => ws.close() }
}
