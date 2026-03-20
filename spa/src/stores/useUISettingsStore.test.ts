import { describe, it, expect, beforeEach } from 'vitest'
import { useUISettingsStore } from './useUISettingsStore'

describe('useUISettingsStore', () => {
  beforeEach(() => {
    useUISettingsStore.setState({
      terminalRevealDelay: 300,
      terminalRenderer: 'webgl',
      keepAliveCount: 0,
      keepAlivePinned: false,
      terminalSettingsVersion: 0,
    })
  })

  it('defaults terminalRenderer to webgl', () => {
    expect(useUISettingsStore.getState().terminalRenderer).toBe('webgl')
  })

  it('can set terminalRenderer to dom', () => {
    useUISettingsStore.getState().setTerminalRenderer('dom')
    expect(useUISettingsStore.getState().terminalRenderer).toBe('dom')
  })

  it('persists terminalRenderer across setState', () => {
    useUISettingsStore.getState().setTerminalRenderer('dom')
    useUISettingsStore.getState().setTerminalRenderer('webgl')
    expect(useUISettingsStore.getState().terminalRenderer).toBe('webgl')
  })
})

describe('keep-alive settings', () => {
  beforeEach(() => {
    useUISettingsStore.setState({
      terminalRevealDelay: 300,
      terminalRenderer: 'webgl',
      keepAliveCount: 0,
      keepAlivePinned: false,
      terminalSettingsVersion: 0,
    })
  })

  it('keepAliveCount defaults to 0', () => {
    expect(useUISettingsStore.getState().keepAliveCount).toBe(0)
  })

  it('keepAlivePinned defaults to false', () => {
    expect(useUISettingsStore.getState().keepAlivePinned).toBe(false)
  })

  it('setKeepAliveCount updates value', () => {
    useUISettingsStore.getState().setKeepAliveCount(3)
    expect(useUISettingsStore.getState().keepAliveCount).toBe(3)
  })

  it('setKeepAlivePinned updates value', () => {
    useUISettingsStore.getState().setKeepAlivePinned(true)
    expect(useUISettingsStore.getState().keepAlivePinned).toBe(true)
  })
})

describe('terminalSettingsVersion', () => {
  beforeEach(() => {
    useUISettingsStore.setState({ terminalSettingsVersion: 0 })
  })

  it('defaults to 0', () => {
    expect(useUISettingsStore.getState().terminalSettingsVersion).toBe(0)
  })

  it('bumpTerminalSettingsVersion increments', () => {
    useUISettingsStore.getState().bumpTerminalSettingsVersion()
    expect(useUISettingsStore.getState().terminalSettingsVersion).toBe(1)
    useUISettingsStore.getState().bumpTerminalSettingsVersion()
    expect(useUISettingsStore.getState().terminalSettingsVersion).toBe(2)
  })
})
