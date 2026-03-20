import { useEffect, useState, useRef, useCallback } from 'react'
import { useTerminal } from '../hooks/useTerminal'
import { useTerminalWs } from '../hooks/useTerminalWs'
import '@xterm/xterm/css/xterm.css'

interface Props {
  wsUrl: string
  visible?: boolean
  connectingMessage?: string
}

export default function TerminalView({ wsUrl, visible = true, connectingMessage }: Props) {
  const { termRef, fitAddonRef, containerRef } = useTerminal()
  const [ready, setReady] = useState(false)
  const [disconnected, setDisconnected] = useState(false)
  const prevVisible = useRef(visible)

  const handleReady = useCallback(() => { setReady(true) }, [])
  const handleDisconnect = useCallback(() => { setDisconnected(true) }, [])
  const handleReconnect = useCallback(() => { setDisconnected(false) }, [])

  const connRef = useTerminalWs({
    wsUrl,
    termRef,
    fitAddonRef,
    containerRef,
    onReady: handleReady,
    onDisconnect: handleDisconnect,
    onReconnect: handleReconnect,
  })

  // Reset state on wsUrl change. React guarantees effects fire in declaration
  // order, so useTerminal (mount) → useTerminalWs (connect) → this reset.
  useEffect(() => {
    setReady(false)
    setDisconnected(false)
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
        if (term && conn) conn.resize(term.cols, term.rows)
      })
      const timer = setTimeout(() => {
        setReady(true)
        termRef.current?.focus()
      }, 500)
      prevVisible.current = visible
      return () => clearTimeout(timer)
    }
    prevVisible.current = visible
  }, [visible, termRef, fitAddonRef, connRef])

  const showOverlay = !ready || disconnected

  return (
    <div className="w-full h-full relative" style={{ background: '#0a0a1a' }}>
      <div ref={containerRef} className="w-full h-full" />
      <div
        data-testid="terminal-overlay"
        className="absolute inset-0 flex items-center justify-center pointer-events-none"
        style={{
          background: disconnected ? 'rgba(10, 10, 26, 0.5)' : '#0a0a1a',
          opacity: showOverlay ? 1 : 0,
          transition: 'opacity 0.3s ease-out',
        }}
      >
        <span className="text-gray-500 text-sm" style={{ animation: 'breathing 2s ease-in-out infinite' }}>
          {disconnected ? 'reconnecting...' : (connectingMessage || 'connecting...')}
        </span>
        <style>{`@keyframes breathing { 0%, 100% { opacity: 0.3; } 50% { opacity: 1; } }`}</style>
      </div>
    </div>
  )
}
