// spa/src/components/MessageBubble.test.tsx
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import MessageBubble from './MessageBubble'

beforeEach(() => { cleanup() })

describe('MessageBubble', () => {
  it('renders user message in a bubble', () => {
    const { container } = render(<MessageBubble role="user" content="hello" />)
    const bubble = container.querySelector('[data-testid="user-bubble"]')
    expect(bubble).toBeInTheDocument()
    expect(bubble).toHaveTextContent('hello')
  })

  it('renders assistant message without bubble wrapper', () => {
    const { container } = render(<MessageBubble role="assistant" content="hi there" />)
    expect(container.querySelector('[data-testid="user-bubble"]')).toBeNull()
    const text = container.querySelector('[data-testid="assistant-text"]')
    expect(text).toBeInTheDocument()
  })

  it('renders assistant markdown with code blocks', () => {
    render(<MessageBubble role="assistant" content="use `npm install`" />)
    expect(screen.getByText('npm install')).toBeInTheDocument()
  })

  it('applies correct user bubble classes', () => {
    const { container } = render(<MessageBubble role="user" content="test" />)
    const bubble = container.querySelector('[data-testid="user-bubble"]')
    expect(bubble?.className).toContain('bg-[#334a5e]')
  })

  it('renders user message as plain text (not markdown)', () => {
    render(<MessageBubble role="user" content="Hello, world!" />)
    expect(screen.getByText('Hello, world!')).toBeInTheDocument()
  })

  it('renders assistant markdown bold content', () => {
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

  it('does not render any avatar elements', () => {
    const { container: userContainer } = render(
      <MessageBubble role="user" content="test" />,
    )
    expect(userContainer.querySelector('[data-testid="icon-user"]')).toBeNull()
    expect(userContainer.querySelector('[data-testid="icon-assistant"]')).toBeNull()

    cleanup()

    const { container: assistantContainer } = render(
      <MessageBubble role="assistant" content="test" />,
    )
    expect(assistantContainer.querySelector('[data-testid="icon-user"]')).toBeNull()
    expect(assistantContainer.querySelector('[data-testid="icon-assistant"]')).toBeNull()
  })
})
