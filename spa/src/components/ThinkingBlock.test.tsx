// spa/src/components/ThinkingBlock.test.tsx
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import ThinkingBlock from './ThinkingBlock'

beforeEach(() => { cleanup() })

describe('ThinkingBlock', () => {
  it('renders collapsed by default showing Thinking header', () => {
    render(<ThinkingBlock content="Let me analyze..." />)
    expect(screen.getByText('Thinking...')).toBeInTheDocument()
    expect(screen.queryByTestId('thinking-content')).toBeNull()
  })

  it('expands on click to show thinking content', () => {
    render(<ThinkingBlock content="Let me analyze this problem." />)
    fireEvent.click(screen.getByTestId('thinking-header'))
    expect(screen.getByTestId('thinking-content')).toHaveTextContent('Let me analyze this problem.')
  })

  it('collapses again on second click', () => {
    render(<ThinkingBlock content="content" />)
    fireEvent.click(screen.getByTestId('thinking-header'))
    expect(screen.getByTestId('thinking-content')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('thinking-header'))
    expect(screen.queryByTestId('thinking-content')).toBeNull()
  })
})
