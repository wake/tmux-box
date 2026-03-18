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
): TerminalConnection {
  let closed = false
  let retryMs = 1000
  let ws: WebSocket

  function connect() {
    ws = new WebSocket(url)
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
      if (!closed) {
        setTimeout(() => {
          if (!closed) connect()
        }, retryMs)
        retryMs = Math.min(retryMs * 2, 30000)
      }
    }
  }

  connect()

  return {
    send: (data) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(data)
    },
    resize: (cols, rows) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'resize', cols, rows }))
      }
    },
    close: () => {
      closed = true
      ws.close()
    },
  }
}
