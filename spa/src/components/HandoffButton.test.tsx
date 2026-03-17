// spa/src/components/HandoffButton.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import HandoffButton from './HandoffButton'

beforeEach(() => {
  cleanup()
})

describe('HandoffButton', () => {
  it('renders button with preset name', () => {
    render(<HandoffButton presetName="cc" state="idle" onHandoff={() => {}} />)
    expect(screen.getByText('Start cc')).toBeInTheDocument()
  })

  it('shows connecting state', () => {
    render(<HandoffButton presetName="cc" state="handoff-in-progress" onHandoff={() => {}} />)
    expect(screen.getByText('Connecting...')).toBeInTheDocument()
  })

  it('disables button during handoff-in-progress', () => {
    render(<HandoffButton presetName="cc" state="handoff-in-progress" onHandoff={() => {}} />)
    expect(screen.getByRole('button')).toBeDisabled()
  })

  it('hidden when connected', () => {
    const { container } = render(
      <HandoffButton presetName="cc" state="connected" onHandoff={() => {}} />,
    )
    expect(container.firstChild).toBeNull()
  })

  it('calls onHandoff when clicked', () => {
    const fn = vi.fn()
    render(<HandoffButton presetName="cc" state="idle" onHandoff={fn} />)
    fireEvent.click(screen.getByRole('button'))
    expect(fn).toHaveBeenCalled()
  })

  it('shows disconnect message in disconnected state', () => {
    render(<HandoffButton presetName="cc" state="disconnected" onHandoff={() => {}} />)
    expect(screen.getByText(/disconnected/i)).toBeInTheDocument()
    expect(screen.getByText('Start cc')).toBeInTheDocument()
  })
})
