// spa/src/components/FileAttachment.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import FileAttachment from './FileAttachment'

beforeEach(() => {
  cleanup()
})

describe('FileAttachment', () => {
  it('renders attachment items', () => {
    const files = [
      { name: 'test.png', type: 'image/png', url: 'data:image/png;base64,abc' },
      { name: 'code.ts', type: 'text/typescript', url: '' },
    ]
    render(<FileAttachment files={files} onRemove={() => {}} />)
    expect(screen.getByText('test.png')).toBeInTheDocument()
    expect(screen.getByText('code.ts')).toBeInTheDocument()
  })

  it('calls onRemove with index', () => {
    const onRemove = vi.fn()
    const files = [{ name: 'test.png', type: 'image/png', url: '' }]
    render(<FileAttachment files={files} onRemove={onRemove} />)
    fireEvent.click(screen.getByLabelText('remove'))
    expect(onRemove).toHaveBeenCalledWith(0)
  })

  it('hidden when no files', () => {
    const { container } = render(<FileAttachment files={[]} onRemove={() => {}} />)
    expect(container.firstChild).toBeNull()
  })

  it('shows image thumbnail for image files with url', () => {
    const files = [
      { name: 'photo.jpg', type: 'image/jpeg', url: 'data:image/jpeg;base64,xyz' },
    ]
    const { container } = render(<FileAttachment files={files} onRemove={() => {}} />)
    const thumbnail = container.querySelector('[style*="background-image"]')
    expect(thumbnail).toBeInTheDocument()
  })

  it('shows file icon for non-image files', () => {
    const files = [{ name: 'readme.md', type: 'text/markdown', url: '' }]
    render(<FileAttachment files={files} onRemove={() => {}} />)
    expect(screen.getByTestId('file-icon')).toBeInTheDocument()
  })

  it('renders multiple remove buttons for multiple files', () => {
    const onRemove = vi.fn()
    const files = [
      { name: 'a.txt', type: 'text/plain', url: '' },
      { name: 'b.txt', type: 'text/plain', url: '' },
    ]
    render(<FileAttachment files={files} onRemove={onRemove} />)
    const removeButtons = screen.getAllByLabelText('remove')
    expect(removeButtons).toHaveLength(2)
    fireEvent.click(removeButtons[1])
    expect(onRemove).toHaveBeenCalledWith(1)
  })
})
