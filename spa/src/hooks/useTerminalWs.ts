import { useEffect, useRef } from 'react'
import type { Terminal } from '@xterm/xterm'
import type { FitAddon } from '@xterm/addon-fit'
import { connectTerminal } from '../lib/ws'
import { useHostStore } from '../stores/useHostStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { canAttachTerminal } from '../lib/rebuild/attach-gate'

interface UseTerminalWsOpts {
  wsUrl: string
  termRef: React.RefObject<Terminal | null>
  fitAddonRef: React.RefObject<FitAddon | null>
  containerRef: React.RefObject<HTMLDivElement | null>
  hostId?: string
  onReady: () => void
  onDisconnect: () => void
  onReconnect: () => void
  getTicket?: () => Promise<string>
}

export function useTerminalWs({ wsUrl, termRef, fitAddonRef, containerRef, hostId, onReady, onDisconnect, onReconnect, getTicket }: UseTerminalWsOpts) {
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

  // Attach gate (spec §4.6): no terminal socket for this host until its current
  // host-events connection has reconciled a `sessions` payload, so the pane can
  // never attach to a session code a tmux restart handed to a stranger.
  //
  // Sticky per wsUrl on purpose. The gate closes again on every host-events
  // reconnect; tearing an already-verified terminal down for that would flicker
  // every pane on a transient blip. Later attempts are re-gated live through
  // `canReconnect` below, which is the path that actually re-opens a socket.
  const gateOpen = useHostStore((s) => (hostId ? s.runtime[hostId]?.attachReady === true : true))
  const gateLatchRef = useRef({ wsUrl, allowed: false })
  if (gateLatchRef.current.wsUrl !== wsUrl) gateLatchRef.current = { wsUrl, allowed: false }
  if (gateOpen) gateLatchRef.current.allowed = true
  const attachAllowed = gateLatchRef.current.allowed

  useEffect(() => {
    const term = termRef.current
    const container = containerRef.current
    if (!term || !container) return
    if (!attachAllowed) return

    let revealed = false
    const reveal = () => {
      if (revealed) return
      revealed = true
      onReadyRef.current()
      term.focus()
    }

    const canReconnect = hostId
      ? () => {
          const state = useHostStore.getState()
          if (!state.hosts[hostId]) return false // host deleted — stop reconnecting
          const runtime = state.runtime[hostId]
          if (runtime && runtime.status !== 'connected') return false
          return canAttachTerminal(hostId)
        }
      : undefined

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
      canReconnect,
      getTicket,
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
  // Other deps (containerRef, fitAddonRef, getTicket, hostId, termRef) are stable refs or
  // props captured once at mount; callbacks stabilized via refs above.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [wsUrl, attachAllowed])

  return connRef
}
