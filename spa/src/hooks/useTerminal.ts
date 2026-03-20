import { useEffect, useRef } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebglAddon } from '@xterm/addon-webgl'
import { Unicode11Addon } from '@xterm/addon-unicode11'
import { WebLinksAddon } from '@xterm/addon-web-links'
import { useUISettingsStore } from '../stores/useUISettingsStore'

export interface UseTerminalResult {
  termRef: React.RefObject<Terminal | null>
  fitAddonRef: React.RefObject<FitAddon | null>
  containerRef: React.RefObject<HTMLDivElement | null>
}

/**
 * xterm instance 的生命週期綁定 component mount/unmount。
 * wsUrl 變更或 settings 變更導致的重建由 TabContent 的 pool key
 * (`key={id}-${poolVersion}`) 觸發 remount，不在此 hook 處理。
 */
export function useTerminal(): UseTerminalResult {
  const containerRef = useRef<HTMLDivElement>(null)
  const termRef = useRef<Terminal | null>(null)
  const fitAddonRef = useRef<FitAddon | null>(null)

  useEffect(() => {
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

    const renderer = useUISettingsStore.getState().terminalRenderer
    if (renderer === 'webgl') {
      try { term.loadAddon(new WebglAddon()) } catch { /* fallback to DOM */ }
    }
    // DOM renderer is the default — no addon needed
    try {
      term.loadAddon(new Unicode11Addon())
      term.unicode.activeVersion = '11'
    } catch { /* fallback to unicode 6 */ }
    try { term.loadAddon(new WebLinksAddon()) } catch { /* non-critical */ }

    // Guard: skip initial fit if container is hidden (display:none in keep-alive pool)
    requestAnimationFrame(() => {
      const { width, height } = container.getBoundingClientRect()
      if (width && height) fitAddon.fit()
    })

    const container = containerRef.current
    let rafId = 0
    const observer = new ResizeObserver((entries) => {
      // Guard against display:none (keep-alive hidden tabs) sending 0-cols resize
      const { width, height } = entries[0]?.contentRect ?? {}
      if (!width || !height) return
      cancelAnimationFrame(rafId)
      rafId = requestAnimationFrame(() => fitAddon.fit())
    })
    observer.observe(container)

    const handleContextMenu = (e: MouseEvent) => e.preventDefault()
    container.addEventListener('contextmenu', handleContextMenu)

    return () => {
      cancelAnimationFrame(rafId)
      observer.disconnect()
      container.removeEventListener('contextmenu', handleContextMenu)
      term.dispose()
      fitAddonRef.current = null
      termRef.current = null
    }
  }, [])

  return { termRef, fitAddonRef, containerRef }
}
