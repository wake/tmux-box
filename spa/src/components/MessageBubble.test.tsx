// spa/src/components/MessageBubble.test.tsx
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import MessageBubble from './MessageBubble'

beforeEach(() => {
  cleanup()
})

describe('MessageBubble', () => {
  it('renders user message as plain text', () => {
    render(<MessageBubble role="user" content="Hello, world!" />)
    expect(screen.getByText('Hello, world!')).toBeInTheDocument()
  })

  it('renders assistant markdown content', () => {
    render(<MessageBubble role="assistant" content="**bold text**" />)
    const bold = document.querySelector('strong')
    expect(bold).toBeInTheDocument()
    expect(bold?.textContent).toBe('bold text')
  })

  it('renders code block for assistant message', () => {
    const code = '```js\nconsole.log("hi")\n```'
    render(<MessageBubble role="assistant" content={code} />)
    const codeEl = document.querySelector('code')
    expect(codeEl).toBeInTheDocument()
  })

  it('shows user icon for user role', () => {
    render(<MessageBubble role="user" content="test" />)
    expect(screen.getByTestId('icon-user')).toBeInTheDocument()
  })

  it('shows robot icon for assistant role', () => {
    render(<MessageBubble role="assistant" content="test" />)
    expect(screen.getByTestId('icon-assistant')).toBeInTheDocument()
  })
})
