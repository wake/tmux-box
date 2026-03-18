// spa/src/components/StreamInput.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import StreamInput from './StreamInput'

beforeEach(() => {
  cleanup()
})

describe('StreamInput', () => {
  it('renders textarea', () => {
    render(<StreamInput onSend={vi.fn()} />)
    expect(screen.getByRole('textbox')).toBeInTheDocument()
  })

  it('calls onSend on Enter key', () => {
    const onSend = vi.fn()
    render(<StreamInput onSend={onSend} />)
    const textarea = screen.getByRole('textbox')
    fireEvent.change(textarea, { target: { value: 'Enter test' } })
    fireEvent.keyDown(textarea, { key: 'Enter', code: 'Enter' })
    expect(onSend).toHaveBeenCalledWith('Enter test')
  })

  it('does NOT send on Shift+Enter', () => {
    const onSend = vi.fn()
    render(<StreamInput onSend={onSend} />)
    const textarea = screen.getByRole('textbox')
    fireEvent.change(textarea, { target: { value: 'multiline' } })
    fireEvent.keyDown(textarea, { key: 'Enter', code: 'Enter', shiftKey: true })
    expect(onSend).not.toHaveBeenCalled()
  })

  it('clears textarea after send', () => {
    render(<StreamInput onSend={vi.fn()} />)
    const textarea = screen.getByRole('textbox') as HTMLTextAreaElement
    fireEvent.change(textarea, { target: { value: 'test message' } })
    fireEvent.keyDown(textarea, { key: 'Enter', code: 'Enter' })
    expect(textarea.value).toBe('')
  })

  it('is disabled when disabled prop is true', () => {
    render(<StreamInput onSend={vi.fn()} disabled />)
    expect(screen.getByRole('textbox')).toBeDisabled()
  })

  it('does not call onSend for empty input', () => {
    const onSend = vi.fn()
    render(<StreamInput onSend={onSend} />)
    const textarea = screen.getByRole('textbox')
    fireEvent.keyDown(textarea, { key: 'Enter', code: 'Enter' })
    expect(onSend).not.toHaveBeenCalled()
  })

  it('renders Handoff to Term button when onHandoffToTerm is provided', () => {
    render(<StreamInput onSend={vi.fn()} onHandoffToTerm={vi.fn()} />)
    expect(screen.getByTitle('Handoff to Term')).toBeInTheDocument()
  })

  it('does not render Handoff to Term button when onHandoffToTerm is not provided', () => {
    render(<StreamInput onSend={vi.fn()} />)
    expect(screen.queryByTitle('Handoff to Term')).not.toBeInTheDocument()
  })

  it('calls onHandoffToTerm when button is clicked', () => {
    const onHandoffToTerm = vi.fn()
    render(<StreamInput onSend={vi.fn()} onHandoffToTerm={onHandoffToTerm} />)
    fireEvent.click(screen.getByTitle('Handoff to Term'))
    expect(onHandoffToTerm).toHaveBeenCalledOnce()
  })

  it('disables Handoff to Term button when disabled prop is true', () => {
    render(<StreamInput onSend={vi.fn()} onHandoffToTerm={vi.fn()} disabled />)
    expect(screen.getByTitle('Handoff to Term')).toBeDisabled()
  })
})
