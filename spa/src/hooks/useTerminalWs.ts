import { useEffect, useRef } from 'react'
import type { Terminal } from '@xterm/xterm'
import type { FitAddon } from '@xterm/addon-fit'
import { connectTerminal } from '../lib/ws'
import { useUISettingsStore } from '../stores/useUISettingsStore'

interface UseTerminalWsOpts {
  wsUrl: string
  termRef: React.RefObject<Terminal | null>
  fitAddonRef: React.RefObject<FitAddon | null>
  containerRef: React.RefObject<HTMLDivElement | null>
  onReady: () => void
  onDisconnect: () => void
  onReconnect: () => void
}

export function useTerminalWs({ wsUrl, termRef, fitAddonRef, containerRef, onReady, onDisconnect, onReconnect }: UseTerminalWsOpts) {
  const connRef = useRef<ReturnType<typeof connectTerminal> | null>(null)
  const revealDelayRef = useRef(useUISettingsStore.getState().terminalRevealDelay)

  // Stabilize callbacks via refs so the WS effect only re-runs on wsUrl change
  const onReadyRef = useRef(onReady)
  const onDisconnectRef = useRef(onDisconnect)
  const onReconnectRef = useRef(onReconnect)
  useEffect(() => {
    onReadyRef.current = onReady
    onDisconnectRef.current = onDisconnect
    onReconnectRef.current = onReconnect
  })

  useEffect(() => {
    return useUISettingsStore.subscribe((s) => { revealDelayRef.current = s.terminalRevealDelay })
  }, [])

  useEffect(() => {
    const term = termRef.current
    const container = containerRef.current
    if (!term || !container) return

    let revealed = false
    const reveal = () => {
      if (revealed) return
      revealed = true
      onReadyRef.current()
      term.focus()
    }

    const conn = connectTerminal(
      wsUrl,
      (data) => {
        term.write(new Uint8Array(data))
        if (!revealed) setTimeout(reveal, revealDelayRef.current)
      },
      () => onDisconnectRef.current(),
      () => {
        // On reconnect, show terminal immediately (buffer already has content).
        // On initial connect, let reveal() handle it after first data + delay.
        onReconnectRef.current()
        if (revealed) onReadyRef.current()
        fitAddonRef.current?.fit()
        conn.resize(term.cols, term.rows)
      },
    )
    connRef.current = conn

    const ta = container.querySelector('.xterm-helper-textarea')

    // --- Shift+Enter: send \n (line feed) instead of \r (carriage return) ---
    // Traditional terminals can't distinguish Shift+Enter from Enter (both
    // send \r). We intercept on the container in capture phase (before xterm.js
    // handles it on the textarea) and send \n directly, which CC accepts as a
    // newline insertion (same as Ctrl+J).
    let shiftEnterHandled = false
    const handleShiftEnter = (ev: Event) => {
      const ke = ev as KeyboardEvent
      if (ke.key === 'Enter' && ke.shiftKey && !ke.ctrlKey && !ke.metaKey) {
        ke.stopPropagation()
        ke.preventDefault()
        shiftEnterHandled = true
        conn.send('\n')
      }
    }
    container.addEventListener('keydown', handleShiftEnter, true)

    // --- IME duplicate guard ---
    // On macOS, pressing Cmd during CJK composition triggers xterm.js
    // _finalizeComposition (first send), then compositionend fires and sends
    // again. Mouse clicks can also re-trigger from residual textarea content.
    // Track last composed text and suppress duplicates until next compositionstart.
    let lastComposedSent = ''
    const handleCompositionStart = () => { lastComposedSent = '' }
    ta?.addEventListener('compositionstart', handleCompositionStart)

    // Suppress \r leaked from xterm.js after our Shift+Enter handler
    const onDataDisp = term.onData((data) => {
      if (shiftEnterHandled && data === '\r') { shiftEnterHandled = false; return }
      shiftEnterHandled = false
      // Suppress IME composition duplicates (same non-escape multi-char data, fixes #21)
      const isComposed = data.length > 1 && data.charCodeAt(0) !== 0x1b
      if (isComposed && data === lastComposedSent) return
      if (isComposed) lastComposedSent = data
      else lastComposedSent = '' // reset on non-composed input (fixes #21)
      conn.send(data)
    })
    const onResizeDisp = term.onResize(({ cols, rows }) => conn.resize(cols, rows))

    return () => {
      onDataDisp.dispose()
      onResizeDisp.dispose()
      container.removeEventListener('keydown', handleShiftEnter, true)
      ta?.removeEventListener('compositionstart', handleCompositionStart)
      conn.close()
      connRef.current = null
    }
  }, [wsUrl]) // Only re-run on wsUrl change; callbacks stabilized via refs

  return connRef
}
