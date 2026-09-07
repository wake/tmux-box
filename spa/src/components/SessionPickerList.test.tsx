import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
import { SessionPickerList } from './SessionPickerList'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'

const HOST_A = 'host-a'
const HOST_B = 'host-b'

beforeEach(() => {
  cleanup()
  useHostStore.setState({
    hosts: {},
    hostOrder: [],
    runtime: {},
    activeHostId: null,
  })
  useSessionStore.setState({
    sessions: {},
    activeHostId: null,
    activeCode: null,
  })
})

describe('SessionPickerList', () => {
  it('shows empty message when no connected hosts', () => {
    const onSelect = vi.fn()
    render(<SessionPickerList onSelect={onSelect} />)
    expect(screen.getByText('No available connections')).toBeInTheDocument()
  })

  it('shows empty message when hosts exist but none are connected', () => {
    useHostStore.setState({
      hosts: {
        [HOST_A]: { id: HOST_A, name: 'Host A', ip: '1.2.3.4', port: 7860, order: 0 },
      },
      hostOrder: [HOST_A],
      runtime: {
        [HOST_A]: { status: 'disconnected' },
      },
    })
    const onSelect = vi.fn()
    render(<SessionPickerList onSelect={onSelect} />)
    expect(screen.getByText('No available connections')).toBeInTheDocument()
  })

  it('shows sessions grouped by host name', () => {
    useHostStore.setState({
      hosts: {
        [HOST_A]: { id: HOST_A, name: 'Host A', ip: '1.2.3.4', port: 7860, order: 0 },
        [HOST_B]: { id: HOST_B, name: 'Host B', ip: '5.6.7.8', port: 7860, order: 1 },
      },
      hostOrder: [HOST_A, HOST_B],
      runtime: {
        [HOST_A]: { status: 'connected' },
        [HOST_B]: { status: 'connected' },
      },
    })
    useSessionStore.setState({
      sessions: {
        [HOST_A]: [
          { code: 'dev001', name: 'dev-session', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false },
        ],
        [HOST_B]: [
          { code: 'cld001', name: 'claude-session', cwd: '/tmp', mode: 'stream', cc_session_id: '', cc_model: '', has_relay: false },
        ],
      },
    })
    const onSelect = vi.fn()
    render(<SessionPickerList onSelect={onSelect} />)

    expect(screen.getByText('Host A')).toBeInTheDocument()
    expect(screen.getByText('Host B')).toBeInTheDocument()
    expect(screen.getByText('dev-session')).toBeInTheDocument()
    expect(screen.getByText('claude-session')).toBeInTheDocument()
  })

  it('skips connected host with no sessions', () => {
    useHostStore.setState({
      hosts: {
        [HOST_A]: { id: HOST_A, name: 'Host A', ip: '1.2.3.4', port: 7860, order: 0 },
      },
      hostOrder: [HOST_A],
      runtime: {
        [HOST_A]: { status: 'connected' },
      },
    })
    useSessionStore.setState({ sessions: { [HOST_A]: [] } })

    const onSelect = vi.fn()
    render(<SessionPickerList onSelect={onSelect} />)

    // Has connected host but no sessions — still shows the wrapper, but no host name
    expect(screen.queryByText('Host A')).not.toBeInTheDocument()
  })

  it('calls onSelect with correct SessionSelection on click', () => {
    useHostStore.setState({
      hosts: {
        [HOST_A]: { id: HOST_A, name: 'Host A', ip: '1.2.3.4', port: 7860, order: 0 },
      },
      hostOrder: [HOST_A],
      runtime: {
        [HOST_A]: { status: 'connected' },
      },
    })
    useSessionStore.setState({
      sessions: {
        [HOST_A]: [
          { code: 'dev001', name: 'dev-session', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false, tmux_instance: '12345:67890' },
        ],
      },
    })

    const onSelect = vi.fn()
    render(<SessionPickerList onSelect={onSelect} />)

    fireEvent.click(screen.getByText('dev-session'))

    expect(onSelect).toHaveBeenCalledTimes(1)
    expect(onSelect).toHaveBeenCalledWith({
      hostId: HOST_A,
      sessionCode: 'dev001',
      cachedName: 'dev-session',
      tmuxInstance: '12345:67890',
    })
  })

  it('uses empty string for tmuxInstance when the session carries none', () => {
    useHostStore.setState({
      hosts: {
        [HOST_A]: { id: HOST_A, name: 'Host A', ip: '1.2.3.4', port: 7860, order: 0 },
      },
      hostOrder: [HOST_A],
      runtime: {
        // A populated runtime.info must NOT be used as the pane's generation:
        // it is ambient state, not the payload that carried the session.
        [HOST_A]: {
          status: 'connected',
          info: { host_id: HOST_A, tmux_instance: '99999:99999', purdex_version: '1.0', tmux_version: '3.6', os: 'darwin', arch: 'arm64' },
        },
      },
    })
    useSessionStore.setState({
      sessions: {
        [HOST_A]: [
          { code: 'dev001', name: 'dev-session', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false },
        ],
      },
    })

    const onSelect = vi.fn()
    render(<SessionPickerList onSelect={onSelect} />)

    fireEvent.click(screen.getByText('dev-session'))

    expect(onSelect).toHaveBeenCalledWith(
      expect.objectContaining({ tmuxInstance: '' }),
    )
  })

  it('only shows connected hosts, not disconnected ones', () => {
    useHostStore.setState({
      hosts: {
        [HOST_A]: { id: HOST_A, name: 'Host A', ip: '1.2.3.4', port: 7860, order: 0 },
        [HOST_B]: { id: HOST_B, name: 'Host B', ip: '5.6.7.8', port: 7860, order: 1 },
      },
      hostOrder: [HOST_A, HOST_B],
      runtime: {
        [HOST_A]: { status: 'connected' },
        [HOST_B]: { status: 'disconnected' },
      },
    })
    useSessionStore.setState({
      sessions: {
        [HOST_A]: [
          { code: 'dev001', name: 'dev-session', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false },
        ],
        [HOST_B]: [
          { code: 'cld001', name: 'cloud-session', cwd: '/tmp', mode: 'stream', cc_session_id: '', cc_model: '', has_relay: false },
        ],
      },
    })

    const onSelect = vi.fn()
    render(<SessionPickerList onSelect={onSelect} />)

    expect(screen.getByText('Host A')).toBeInTheDocument()
    expect(screen.getByText('dev-session')).toBeInTheDocument()
    expect(screen.queryByText('Host B')).not.toBeInTheDocument()
    expect(screen.queryByText('cloud-session')).not.toBeInTheDocument()
  })
})
