import { useEffect, useRef, useState } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebglAddon } from '@xterm/addon-webgl'
import { connectTerminal } from '../lib/ws'
import '@xterm/xterm/css/xterm.css'

interface Props {
  wsUrl: string
}

export default function TerminalView({ wsUrl }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)
  const [ready, setReady] = useState(false)

  useEffect(() => {
    setReady(false)
    if (!containerRef.current) return

    const term = new Terminal({
      theme: { background: '#0a0a1a', foreground: '#e0e0e0' },
      fontSize: 14,
      fontFamily: 'Menlo, Monaco, monospace',
      cursorBlink: true,
    })

    const fitAddon = new FitAddon()
    term.loadAddon(fitAddon)
    term.open(containerRef.current)

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

    term.onData((data) => conn.send(data))
    term.onResize(({ cols, rows }) => conn.resize(cols, rows))

    let rafId = 0
    const observer = new ResizeObserver(() => {
      cancelAnimationFrame(rafId)
      rafId = requestAnimationFrame(() => fitAddon.fit())
    })
    observer.observe(containerRef.current)

    return () => {
      clearTimeout(fallbackTimer)
      cancelAnimationFrame(rafId)
      observer.disconnect()
      conn.close()
      term.dispose()
    }
  }, [wsUrl])

  return (
    <div className="w-full h-full relative" style={{ background: '#0a0a1a' }}>
      <div ref={containerRef} className="w-full h-full" />

      {/* Loading overlay with breathing animation */}
      <div
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
