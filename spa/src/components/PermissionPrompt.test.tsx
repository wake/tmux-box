// spa/src/components/PermissionPrompt.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import PermissionPrompt from './PermissionPrompt'

beforeEach(() => {
  cleanup()
})

describe('PermissionPrompt', () => {
  it('shows tool name and description in horizontal layout', () => {
    render(
      <PermissionPrompt
        tool="Bash"
        description="Run shell commands"
        onAllow={vi.fn()}
        onDeny={vi.fn()}
      />
    )
    expect(screen.getByText('Bash')).toBeInTheDocument()
    expect(screen.getByText('Run shell commands')).toBeInTheDocument()
    // Horizontal layout: tool name and buttons should be in the same flex row
    const container = screen.getByText('Bash').closest('[class*="flex items-center gap-3"]')
    expect(container).toBeInTheDocument()
  })

  it('calls onAllow when Allow is clicked', () => {
    const onAllow = vi.fn()
    render(
      <PermissionPrompt
        tool="Bash"
        description="Run shell commands"
        onAllow={onAllow}
        onDeny={vi.fn()}
      />
    )
    fireEvent.click(screen.getByRole('button', { name: 'Allow' }))
    expect(onAllow).toHaveBeenCalledOnce()
  })

  it('calls onDeny when Deny is clicked', () => {
    const onDeny = vi.fn()
    render(
      <PermissionPrompt
        tool="Bash"
        description="Run shell commands"
        onAllow={vi.fn()}
        onDeny={onDeny}
      />
    )
    fireEvent.click(screen.getByRole('button', { name: 'Deny' }))
    expect(onDeny).toHaveBeenCalledOnce()
  })

  it('shows WarningCircle icon', () => {
    render(
      <PermissionPrompt
        tool="Read"
        description="Read files"
        onAllow={vi.fn()}
        onDeny={vi.fn()}
      />
    )
    expect(screen.getByTestId('warning-icon')).toBeInTheDocument()
  })

  it('renders Allow and Deny buttons side by side', () => {
    render(
      <PermissionPrompt
        tool="Bash"
        description="Run shell commands"
        onAllow={vi.fn()}
        onDeny={vi.fn()}
      />
    )
    const allowBtn = screen.getByRole('button', { name: 'Allow' })
    const denyBtn = screen.getByRole('button', { name: 'Deny' })
    // Both buttons should share the same parent flex container
    expect(allowBtn.parentElement).toBe(denyBtn.parentElement)
  })
})
