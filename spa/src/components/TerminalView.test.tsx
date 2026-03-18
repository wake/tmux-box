import { describe, it, expect, vi } from 'vitest'
import { render } from '@testing-library/react'
import TerminalView from './TerminalView'

const { mockClose, TerminalSpy } = vi.hoisted(() => {
  const mockClose = vi.fn()
  const TerminalSpy = vi.fn(function (opts: Record<string, unknown>) {
    ;(this as unknown as Record<string, unknown>)._opts = opts
    return {
      loadAddon: vi.fn(),
      open: vi.fn(),
      write: vi.fn(),
      onData: vi.fn(),
      onResize: vi.fn(),
      dispose: vi.fn(),
      focus: vi.fn(),
      cols: 80,
      rows: 24,
      _opts: opts,
    }
  })
  return { mockClose, TerminalSpy }
})

// xterm.js requires DOM APIs not available in jsdom, so we test mounting only
vi.mock('@xterm/xterm', () => ({
  Terminal: TerminalSpy,
}))

vi.mock('@xterm/addon-fit', () => ({
  FitAddon: vi.fn(function () {
    return {
      fit: vi.fn(),
      dispose: vi.fn(),
    }
  }),
}))

vi.mock('@xterm/addon-webgl', () => ({
  WebglAddon: vi.fn(function () {
    return {
      dispose: vi.fn(),
    }
  }),
}))

vi.mock('../lib/ws', () => ({
  connectTerminal: vi.fn().mockReturnValue({
    send: vi.fn(),
    resize: vi.fn(),
    close: mockClose,
  }),
}))

describe('TerminalView', () => {
  it('renders container div', () => {
    const { container } = render(
      <TerminalView wsUrl="ws://localhost:7860/ws/terminal/test" />
    )
    expect(container.querySelector('div')).toBeInTheDocument()
  })

  it('cleans up on unmount', () => {
    mockClose.mockClear()
    const { unmount } = render(
      <TerminalView wsUrl="ws://localhost:7860/ws/terminal/test" />
    )
    unmount()
    expect(mockClose).toHaveBeenCalled()
  })

  it('creates Terminal with macOptionClickForcesSelection enabled', () => {
    TerminalSpy.mockClear()
    render(<TerminalView wsUrl="ws://localhost:7860/ws/terminal/test" />)
    expect(TerminalSpy).toHaveBeenCalled()
    const opts = TerminalSpy.mock.calls[0][0]
    expect(opts.macOptionClickForcesSelection).toBe(true)
  })

  it('creates Terminal with rightClickSelectsWord enabled', () => {
    TerminalSpy.mockClear()
    render(<TerminalView wsUrl="ws://localhost:7860/ws/terminal/test" />)
    const opts = TerminalSpy.mock.calls[0][0]
    expect(opts.rightClickSelectsWord).toBe(true)
  })

  it('shows overlay when visible changes from false to true', () => {
    const { container, rerender } = render(
      <TerminalView wsUrl="ws://localhost:7860/ws/terminal/test" visible={false} />
    )
    // Rerender with visible=true — overlay should be visible (opacity 1)
    rerender(<TerminalView wsUrl="ws://localhost:7860/ws/terminal/test" visible={true} />)
    const overlay = container.querySelector('[data-testid="terminal-overlay"]')
    expect(overlay).toBeInTheDocument()
    expect(overlay?.getAttribute('style')).toContain('opacity: 1')
  })
})
