// spa/src/components/AskUserQuestion.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import AskUserQuestion from './AskUserQuestion'

beforeEach(() => {
  cleanup()
})

describe('AskUserQuestion', () => {
  const defaultProps = {
    questions: [
      {
        question: 'Which option do you prefer?',
        options: [
          { label: 'Option A' },
          { label: 'Option B' },
          { label: 'Option C', description: 'The best one' },
        ],
        multiSelect: false,
      },
    ],
    onSubmit: vi.fn(),
    onCancel: vi.fn(),
  }

  it('renders the question text', () => {
    render(<AskUserQuestion {...defaultProps} />)
    expect(screen.getByText('Which option do you prefer?')).toBeInTheDocument()
  })

  it('renders all options', () => {
    render(<AskUserQuestion {...defaultProps} />)
    expect(screen.getByText('Option A')).toBeInTheDocument()
    expect(screen.getByText('Option B')).toBeInTheDocument()
    expect(screen.getByText('Option C')).toBeInTheDocument()
  })

  it('submits selected single option on Enter key', () => {
    const onSubmit = vi.fn()
    render(<AskUserQuestion {...defaultProps} onSubmit={onSubmit} />)
    fireEvent.click(screen.getByText('Option B'))
    fireEvent.keyDown(screen.getByText('Option B').closest('[data-testid="ask-container"]')!, {
      key: 'Enter',
    })
    expect(onSubmit).toHaveBeenCalledWith('Option B')
  })

  it('calls onCancel on Escape key', () => {
    const onCancel = vi.fn()
    render(<AskUserQuestion {...defaultProps} onCancel={onCancel} />)
    fireEvent.keyDown(screen.getByTestId('ask-container'), { key: 'Escape' })
    expect(onCancel).toHaveBeenCalledOnce()
  })

  it('submits comma-separated labels for multi-select on Enter', () => {
    const onSubmit = vi.fn()
    render(
      <AskUserQuestion
        questions={[
          {
            question: 'Pick multiple',
            options: [
              { label: 'Alpha' },
              { label: 'Beta' },
              { label: 'Gamma' },
            ],
            multiSelect: true,
          },
        ]}
        onSubmit={onSubmit}
        onCancel={vi.fn()}
      />
    )
    fireEvent.click(screen.getByText('Alpha'))
    fireEvent.click(screen.getByText('Gamma'))
    fireEvent.keyDown(screen.getByTestId('ask-container'), { key: 'Enter' })
    expect(onSubmit).toHaveBeenCalledWith('Alpha, Gamma')
  })

  it('renders option descriptions when provided', () => {
    render(<AskUserQuestion {...defaultProps} />)
    expect(screen.getByText(/The best one/)).toBeInTheDocument()
  })

  it('renders fallback for empty questions array', () => {
    render(
      <AskUserQuestion
        questions={[]}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />
    )
    expect(screen.getByText('Please answer:')).toBeInTheDocument()
  })

  it('shows free-text input when no options', () => {
    render(
      <AskUserQuestion
        questions={[{ question: 'What is your name?' }]}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />
    )
    expect(screen.getByPlaceholderText('Type your answer…')).toBeInTheDocument()
  })

  it('submits free-text on Enter', () => {
    const onSubmit = vi.fn()
    render(
      <AskUserQuestion
        questions={[{ question: 'What is your name?' }]}
        onSubmit={onSubmit}
        onCancel={vi.fn()}
      />
    )
    const input = screen.getByPlaceholderText('Type your answer…')
    fireEvent.change(input, { target: { value: 'Claude' } })
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(onSubmit).toHaveBeenCalledWith('Claude')
  })

  it('does not submit empty free-text on Enter', () => {
    const onSubmit = vi.fn()
    render(
      <AskUserQuestion
        questions={[{ question: 'What is your name?' }]}
        onSubmit={onSubmit}
        onCancel={vi.fn()}
      />
    )
    const input = screen.getByPlaceholderText('Type your answer…')
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(onSubmit).not.toHaveBeenCalled()
  })
})
