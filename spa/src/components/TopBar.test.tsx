// spa/src/components/TopBar.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import TopBar from './TopBar'

const defaultProps = {
  sessionName: 'test',
  mode: 'term',
  onModeChange: vi.fn(),
}

beforeEach(() => {
  cleanup()
  defaultProps.onModeChange = vi.fn()
})

describe('TopBar', () => {
  it('shows session name', () => {
    render(<TopBar {...defaultProps} sessionName="my-project" />)
    expect(screen.getByText('my-project')).toBeInTheDocument()
  })

  it('shows term and stream mode buttons', () => {
    render(<TopBar {...defaultProps} />)
    expect(screen.getByTestId('mode-btn-term')).toBeInTheDocument()
    expect(screen.getByTestId('mode-btn-stream')).toBeInTheDocument()
  })

  it('highlights active mode', () => {
    render(<TopBar {...defaultProps} mode="stream" />)
    expect(screen.getByTestId('mode-btn-stream').className).toContain('bg-[#404040]')
    expect(screen.getByTestId('mode-btn-term').className).toContain('text-[#888]')
  })

  it('calls onModeChange when clicking term', () => {
    render(<TopBar {...defaultProps} />)
    fireEvent.click(screen.getByTestId('mode-btn-term'))
    expect(defaultProps.onModeChange).toHaveBeenCalledWith('term')
  })

  it('calls onModeChange when clicking stream', () => {
    render(<TopBar {...defaultProps} />)
    fireEvent.click(screen.getByTestId('mode-btn-stream'))
    expect(defaultProps.onModeChange).toHaveBeenCalledWith('stream')
  })

  it('renders buttons in order: term → stream', () => {
    render(<TopBar {...defaultProps} />)
    const modeSwitch = screen.getByTestId('mode-switch')
    const buttons = modeSwitch.querySelectorAll('button')
    expect(buttons[0].textContent).toContain('term')
    expect(buttons[1].textContent).toContain('stream')
  })
})
