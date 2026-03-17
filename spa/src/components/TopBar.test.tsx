// spa/src/components/TopBar.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import TopBar from './TopBar'

beforeEach(() => {
  cleanup()
})

describe('TopBar', () => {
  it('shows session name', () => {
    render(<TopBar sessionName="my-project" mode="term" onModeChange={vi.fn()} onInterrupt={vi.fn()} />)
    expect(screen.getByText('my-project')).toBeInTheDocument()
  })

  it('shows all three mode buttons', () => {
    render(<TopBar sessionName="test" mode="term" onModeChange={vi.fn()} onInterrupt={vi.fn()} />)
    expect(screen.getByTestId('mode-btn-term')).toBeInTheDocument()
    expect(screen.getByTestId('mode-btn-jsonl')).toBeInTheDocument()
    expect(screen.getByTestId('mode-btn-stream')).toBeInTheDocument()
  })

  it('highlights active mode', () => {
    render(<TopBar sessionName="test" mode="stream" onModeChange={vi.fn()} onInterrupt={vi.fn()} />)
    expect(screen.getByTestId('mode-btn-stream').className).toContain('bg-gray-700')
    expect(screen.getByTestId('mode-btn-term').className).toContain('text-gray-500')
  })

  it('calls onModeChange with target mode', () => {
    const onChange = vi.fn()
    render(<TopBar sessionName="test" mode="term" onModeChange={onChange} onInterrupt={vi.fn()} />)
    fireEvent.click(screen.getByTestId('mode-btn-stream'))
    expect(onChange).toHaveBeenCalledWith('stream')
  })

  it('shows interrupt button only in stream mode', () => {
    const { rerender } = render(
      <TopBar sessionName="test" mode="term" onModeChange={vi.fn()} onInterrupt={vi.fn()} />
    )
    expect(screen.queryByTestId('interrupt-btn')).toBeNull()

    rerender(
      <TopBar sessionName="test" mode="stream" onModeChange={vi.fn()} onInterrupt={vi.fn()} />
    )
    expect(screen.getByTestId('interrupt-btn')).toBeInTheDocument()
  })
})
