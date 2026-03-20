import { describe, it, expect, beforeEach } from 'vitest'
import { useHostStore } from './useHostStore'

describe('useHostStore', () => {
  beforeEach(() => {
    useHostStore.getState().reset()
  })

  it('has a default host', () => {
    const { defaultHost } = useHostStore.getState()
    expect(defaultHost.id).toBe('local')
    expect(defaultHost.name).toBeTruthy()
    expect(defaultHost.address).toBeTruthy()
  })

  it('returns daemon base URL', () => {
    const base = useHostStore.getState().getDaemonBase('local')
    expect(base).toMatch(/^https?:\/\//)
  })

  it('returns ws base URL', () => {
    const wsBase = useHostStore.getState().getWsBase('local')
    expect(wsBase).toMatch(/^wss?:\/\//)
  })

  it('can update default host address', () => {
    useHostStore.getState().updateHost('local', { address: '192.168.1.1', port: 8080 })
    const base = useHostStore.getState().getDaemonBase('local')
    expect(base).toContain('192.168.1.1')
    expect(base).toContain('8080')
  })
})
