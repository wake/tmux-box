import { useEffect, useRef, useState } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebglAddon } from '@xterm/addon-webgl'
import { connectTerminal } from '../lib/ws'
import '@xterm/xterm/css/xterm.css'

interface Props {
  wsUrl: string
  visible?: boolean
}

export default function TerminalView({ wsUrl, visible = true }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)
  const fitAddonRef = useRef<FitAddon | null>(null)
  const connRef = useRef<ReturnType<typeof connectTerminal> | null>(null)
  const termRef = useRef<Terminal | null>(null)
  const [ready, setReady] = useState(false)
  const [disconnected, setDisconnected] = useState(false)
  const prevVisible = useRef(visible)

  // Initial setup — create terminal + WS connection
  useEffect(() => {
    setReady(false)
    setDisconnected(false)
    if (!containerRef.current) return

    const term = new Terminal({
      theme: { background: '#0a0a1a', foreground: '#e0e0e0' },
      fontSize: 14,
      fontFamily: 'Menlo, Monaco, monospace',
      cursorBlink: true,
      macOptionClickForcesSelection: true,
      rightClickSelectsWord: true,
    })

    const fitAddon = new FitAddon()
    term.loadAddon(fitAddon)
    term.open(containerRef.current)
    fitAddonRef.current = fitAddon
    termRef.current = term

    try { term.loadAddon(new WebglAddon()) } catch { /* fallback to canvas */ }

    // Filter out horizontal-dominant wheel events. macOS trackpad often produces
    // an initial event with |deltaX| > |deltaY| when the finger lands slightly
    // off-axis, causing the first scroll to be swallowed or misinterpreted.
    term.attachCustomWheelEventHandler((ev: WheelEvent) => {
      return Math.abs(ev.deltaY) >= Math.abs(ev.deltaX)
    })

    requestAnimationFrame(() => fitAddon.fit())

    let revealed = false
    const reveal = () => {
      if (revealed) return
      revealed = true
      setReady(true)
      term.focus()
    }

    // Fallback: reveal after 1.5s even if no data received
    const fallbackTimer = setTimeout(reveal, 1500)

    const conn = connectTerminal(
      wsUrl,
      (data) => {
        term.write(new Uint8Array(data))
        // Reveal shortly after first data — give tmux time to finish rendering
        if (!revealed) setTimeout(reveal, 300)
      },
      () => setDisconnected(true),
      () => {
        setDisconnected(false)
        // On reconnect, show terminal immediately (buffer already has content).
        // On initial connect, let reveal() handle it after first data + 300ms.
        if (revealed) setReady(true)
        fitAddon.fit()
        conn.resize(term.cols, term.rows)
      },
    )
    connRef.current = conn

    const container = containerRef.current
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

    term.onData((data) => {
      // Suppress \r leaked from xterm.js after our Shift+Enter handler
      if (shiftEnterHandled && data === '\r') {
        shiftEnterHandled = false
        return
      }
      shiftEnterHandled = false

      // Suppress IME composition duplicates (same non-escape multi-char data)
      const isComposed = data.length > 1 && data.charCodeAt(0) !== 0x1b
      if (isComposed && data === lastComposedSent) return
      if (isComposed) lastComposedSent = data
      else lastComposedSent = '' // reset on non-composed input (fixes #21)

      conn.send(data)
    })
    term.onResize(({ cols, rows }) => conn.resize(cols, rows))

    // Suppress browser context menu on the terminal to avoid double-menu
    const handleContextMenu = (e: MouseEvent) => e.preventDefault()
    container.addEventListener('contextmenu', handleContextMenu)

    let rafId = 0
    const observer = new ResizeObserver(() => {
      cancelAnimationFrame(rafId)
      rafId = requestAnimationFrame(() => fitAddon.fit())
    })
    observer.observe(container)

    return () => {
      clearTimeout(fallbackTimer)
      cancelAnimationFrame(rafId)
      observer.disconnect()
      container.removeEventListener('keydown', handleShiftEnter, true)
      ta?.removeEventListener('compositionstart', handleCompositionStart)
      container.removeEventListener('contextmenu', handleContextMenu)
      conn.close()
      term.dispose()
      fitAddonRef.current = null
      connRef.current = null
      termRef.current = null
    }
  }, [wsUrl])

  // Re-show overlay + refit when becoming visible after being hidden
  useEffect(() => {
    if (visible && !prevVisible.current) {
      // Becoming visible — show overlay, refit, then fade out
      setReady(false)
      requestAnimationFrame(() => {
        fitAddonRef.current?.fit()
        // Explicitly send resize even if fit() didn't change dimensions,
        // because tmux window may have been resized during stream mode
        // and onResize won't fire when cols/rows stay the same.
        const term = termRef.current
        const conn = connRef.current
        if (term && conn) {
          conn.resize(term.cols, term.rows)
        }
      })
      const timer = setTimeout(() => {
        setReady(true)
        termRef.current?.focus()
      }, 500)
      prevVisible.current = visible
      return () => clearTimeout(timer)
    }
    prevVisible.current = visible
  }, [visible])

  const showOverlay = !ready || disconnected

  return (
    <div className="w-full h-full relative" style={{ background: '#0a0a1a' }}>
      <div ref={containerRef} className="w-full h-full" />

      {/* Loading / reconnecting overlay */}
      <div
        data-testid="terminal-overlay"
        className="absolute inset-0 flex items-center justify-center pointer-events-none"
        style={{
          background: disconnected ? 'rgba(10, 10, 26, 0.5)' : '#0a0a1a',
          opacity: showOverlay ? 1 : 0,
          transition: 'opacity 0.3s ease-out',
        }}
      >
        <span
          className="text-gray-500 text-sm"
          style={{ animation: 'breathing 2s ease-in-out infinite' }}
        >
          {disconnected ? 'reconnecting...' : 'connecting...'}
        </span>
        <style>{`
          @keyframes breathing {
            0%, 100% { opacity: 0.3; }
            50% { opacity: 1; }
          }
        `}</style>
      </div>
    </div>
  )
}
