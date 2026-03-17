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

  it('submits selected single option', () => {
    const onSubmit = vi.fn()
    render(<AskUserQuestion {...defaultProps} onSubmit={onSubmit} />)
    fireEvent.click(screen.getByText('Option B'))
    fireEvent.click(screen.getByTestId('submit-btn'))
    expect(onSubmit).toHaveBeenCalledWith('Option B')
  })

  it('calls onCancel when cancel is clicked', () => {
    const onCancel = vi.fn()
    render(<AskUserQuestion {...defaultProps} onCancel={onCancel} />)
    fireEvent.click(screen.getByTestId('cancel-btn'))
    expect(onCancel).toHaveBeenCalledOnce()
  })

  it('submits comma-separated labels for multi-select', () => {
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
    fireEvent.click(screen.getByTestId('submit-btn'))
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
})
