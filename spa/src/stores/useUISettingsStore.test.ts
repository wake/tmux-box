import { describe, it, expect, beforeEach } from 'vitest'
import { useUISettingsStore } from './useUISettingsStore'

describe('useUISettingsStore', () => {
  beforeEach(() => {
    useUISettingsStore.setState({
      terminalRevealDelay: 300,
      terminalRenderer: 'webgl',
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
