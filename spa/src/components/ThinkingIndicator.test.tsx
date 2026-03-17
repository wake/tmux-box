// spa/src/components/ThinkingIndicator.test.tsx
import { describe, it, expect } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import ThinkingIndicator from './ThinkingIndicator'

describe('ThinkingIndicator', () => {
  it('renders dots when visible', () => {
    render(<ThinkingIndicator visible={true} />)
    const dots = screen.getByTestId('thinking-indicator')
    expect(dots).toBeInTheDocument()
  })

  it('hidden when not visible', () => {
    cleanup()
    render(<ThinkingIndicator visible={false} />)
    expect(screen.queryByTestId('thinking-indicator')).not.toBeInTheDocument()
  })
})
