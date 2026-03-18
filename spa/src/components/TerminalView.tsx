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
  const prevVisible = useRef(visible)

  // Initial setup — create terminal + WS connection
  useEffect(() => {
    setReady(false)
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
      () => term.write('\r\n\x1b[31m[disconnected]\x1b[0m\r\n'),
      () => {
        fitAddon.fit()
        conn.resize(term.cols, term.rows)
      },
    )
    connRef.current = conn

    term.onData((data) => conn.send(data))
    term.onResize(({ cols, rows }) => conn.resize(cols, rows))

    // Suppress browser context menu on the terminal to avoid double-menu
    const container = containerRef.current
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

  return (
    <div className="w-full h-full relative" style={{ background: '#0a0a1a' }}>
      <div ref={containerRef} className="w-full h-full" />

      {/* Loading overlay with breathing animation */}
      <div
        data-testid="terminal-overlay"
        className="absolute inset-0 flex items-center justify-center pointer-events-none"
        style={{
          background: '#0a0a1a',
          opacity: ready ? 0 : 1,
          transition: 'opacity 0.3s ease-out',
        }}
      >
        <span
          className="text-gray-500 text-sm"
          style={{ animation: 'breathing 2s ease-in-out infinite' }}
        >
          connecting...
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
