export interface TerminalConnection {
  send: (data: string) => void
  resize: (cols: number, rows: number) => void
  close: () => void
}

export function connectTerminal(
  url: string,
  onData: (data: ArrayBuffer) => void,
  onClose: () => void,
  onOpen?: () => void,
  canReconnect?: () => boolean,
  getTicket?: () => Promise<string>,
): TerminalConnection {
  let closed = false
  let retryMs = 1000
  let ws: WebSocket

  // Async ticket path — separate from sync connect to avoid breaking existing sync callers
  async function connectWithTicket() {
    let wsUrl = url
    try {
      const ticket = await getTicket!()
      const u = new URL(wsUrl)
      u.searchParams.set('ticket', ticket)
      wsUrl = u.toString()
    } catch {
      scheduleRetry()
      return
    }
    if (closed) return
    // The gate can close while the ticket is in flight (the host-events
    // connection dropped, or restarted and has not re-delivered its session
    // list). Re-check before the socket exists, or the attach the gate was
    // meant to stop happens anyway.
    if (canReconnect && !canReconnect()) {
      scheduleRetry()
      return
    }
    setupWs(wsUrl)
  }

  function scheduleRetry() {
    const delay = retryMs
    retryMs = Math.min(retryMs * 2, 30000)
    // Inner loop: poll at fixed `delay` while gate is closed, then connect
    // once the gate opens.  backoff only advances on the next scheduleRetry
    // call (i.e. after a real connection failure), not during gate polling.
    function attempt() {
      setTimeout(() => {
        if (closed) return
        if (canReconnect && !canReconnect()) {
          attempt()
          return
        }
        connect()
      }, delay)
    }
    attempt()
  }

  function setupWs(wsUrl: string) {
    ws = new WebSocket(wsUrl)
    ws.binaryType = 'arraybuffer'

    ws.onopen = () => {
      retryMs = 1000 // reset backoff on success
      onOpen?.()
    }
    ws.onmessage = (e) => {
      if (e.data instanceof ArrayBuffer) onData(e.data)
    }
    ws.onerror = () => {}
    ws.onclose = () => {
      if (closed) return // manual close — don't notify or reconnect
      onClose()
      scheduleRetry()
    }
  }

  function connect() {
    if (getTicket) {
      connectWithTicket()
      return
    }
    setupWs(url)
  }

  connect()

  return {
    send: (data) => {
      if (ws?.readyState === WebSocket.OPEN) ws.send(data)
    },
    resize: (cols, rows) => {
      if (ws?.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'resize', cols, rows }))
      }
    },
    close: () => {
      closed = true
      ws?.close()
    },
  }
}
