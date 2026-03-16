// spa/src/components/PermissionPrompt.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import PermissionPrompt from './PermissionPrompt'

beforeEach(() => {
  cleanup()
})

describe('PermissionPrompt', () => {
  it('shows tool name and description', () => {
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
    fireEvent.click(screen.getByTestId('allow-btn'))
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
    fireEvent.click(screen.getByTestId('deny-btn'))
    expect(onDeny).toHaveBeenCalledOnce()
  })

  it('shows ShieldWarning icon', () => {
    render(
      <PermissionPrompt
        tool="Read"
        description="Read files"
        onAllow={vi.fn()}
        onDeny={vi.fn()}
      />
    )
    expect(screen.getByTestId('shield-icon')).toBeInTheDocument()
  })
})
