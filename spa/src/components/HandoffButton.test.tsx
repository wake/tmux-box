// spa/src/components/HandoffButton.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import HandoffButton from './HandoffButton'

beforeEach(() => {
  cleanup()
})

describe('HandoffButton', () => {
  it('renders button with Handoff label when CC is running', () => {
    render(<HandoffButton state="idle" sessionStatus="cc-idle" onHandoff={() => {}} />)
    expect(screen.getByText('Handoff')).toBeInTheDocument()
  })

  it('shows connecting state', () => {
    render(<HandoffButton state="handoff-in-progress" sessionStatus="cc-idle" onHandoff={() => {}} />)
    expect(screen.getByText('Connecting...')).toBeInTheDocument()
  })

  it('disables button during handoff-in-progress', () => {
    render(<HandoffButton state="handoff-in-progress" sessionStatus="cc-idle" onHandoff={() => {}} />)
    expect(screen.getByRole('button')).toBeDisabled()
  })

  it('hidden when connected', () => {
    const { container } = render(
      <HandoffButton state="connected" sessionStatus="cc-idle" onHandoff={() => {}} />,
    )
    expect(container.firstChild).toBeNull()
  })

  it('calls onHandoff when clicked with CC running', () => {
    const fn = vi.fn()
    render(<HandoffButton state="idle" sessionStatus="cc-running" onHandoff={fn} />)
    fireEvent.click(screen.getByRole('button'))
    expect(fn).toHaveBeenCalled()
  })

  it('shows disconnect message in disconnected state', () => {
    render(<HandoffButton state="disconnected" sessionStatus="cc-idle" onHandoff={() => {}} />)
    expect(screen.getByText(/disconnected/i)).toBeInTheDocument()
    expect(screen.getByText('Handoff')).toBeInTheDocument()
  })

  it('disables button when no CC running', () => {
    render(<HandoffButton state="idle" sessionStatus="shell" onHandoff={() => {}} />)
    expect(screen.getByRole('button')).toBeDisabled()
    expect(screen.getByText('No CC running')).toBeInTheDocument()
  })

  it('shows progress label for detecting', () => {
    render(<HandoffButton state="handoff-in-progress" progress="detecting" sessionStatus="cc-idle" onHandoff={() => {}} />)
    expect(screen.getByText('Detecting CC...')).toBeInTheDocument()
  })

  it('shows progress label for stopping-cc', () => {
    render(<HandoffButton state="handoff-in-progress" progress="stopping-cc" sessionStatus="cc-idle" onHandoff={() => {}} />)
    expect(screen.getByText('Stopping CC...')).toBeInTheDocument()
  })

  it('shows progress label for launching', () => {
    render(<HandoffButton state="handoff-in-progress" progress="launching" sessionStatus="cc-idle" onHandoff={() => {}} />)
    expect(screen.getByText('Launching relay...')).toBeInTheDocument()
  })

  it('shows progress label for extracting-id', () => {
    render(<HandoffButton state="handoff-in-progress" progress="extracting-id" sessionStatus="cc-idle" onHandoff={() => {}} />)
    expect(screen.getByText('Extracting session...')).toBeInTheDocument()
  })

  it('shows progress label for exiting-cc', () => {
    render(<HandoffButton state="handoff-in-progress" progress="exiting-cc" sessionStatus="cc-idle" onHandoff={() => {}} />)
    expect(screen.getByText('Exiting CC...')).toBeInTheDocument()
  })

  it('falls back to Connecting... with empty progress', () => {
    render(<HandoffButton state="handoff-in-progress" progress="" sessionStatus="cc-idle" onHandoff={() => {}} />)
    expect(screen.getByText('Connecting...')).toBeInTheDocument()
  })
})
