import { describe, it, expect } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { StatusBar } from './StatusBar'

describe('StatusBar', () => {
  it('renders host and session info', () => {
    cleanup()
    render(<StatusBar hostName="mlab" sessionName="dev-server" status="connected" mode="term" />)
    expect(screen.getByText('mlab')).toBeTruthy()
    expect(screen.getByText('dev-server')).toBeTruthy()
    expect(screen.getByText('connected')).toBeTruthy()
    expect(screen.getByText('term')).toBeTruthy()
  })

  it('renders empty state when no session', () => {
    cleanup()
    render(<StatusBar hostName={null} sessionName={null} status={null} mode={null} />)
    expect(screen.getByText('No active session')).toBeTruthy()
  })
})
