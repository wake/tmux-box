import { describe, it, expect, beforeEach } from 'vitest'
import { registerTabRenderer, getTabRenderer, getTabIcon, clearRegistry } from './tab-registry'
import type { Tab } from '../types/tab'

const mockComponent = () => null

describe('tab-registry', () => {
  beforeEach(() => clearRegistry())

  it('registers and retrieves a renderer', () => {
    registerTabRenderer('session', {
      component: mockComponent,
      viewModes: ['terminal', 'stream'],
      defaultViewMode: 'terminal',
      icon: () => 'Terminal',
    })
    const config = getTabRenderer('session')
    expect(config).toBeDefined()
    expect(config!.viewModes).toEqual(['terminal', 'stream'])
    expect(config!.defaultViewMode).toBe('terminal')
  })

  it('returns undefined for unregistered type', () => {
    expect(getTabRenderer('unknown')).toBeUndefined()
  })

  it('getTabIcon returns dynamic icon based on viewMode', () => {
    registerTabRenderer('session', {
      component: mockComponent,
      icon: (tab) => tab.viewMode === 'stream' ? 'ChatCircleDots' : 'Terminal',
    })
    const termTab: Tab = { id: '1', type: 'session', label: 't', icon: '', hostId: 'h', viewMode: 'terminal', data: {}, pinned: false, locked: false }
    const streamTab: Tab = { id: '2', type: 'session', label: 't', icon: '', hostId: 'h', viewMode: 'stream', data: {}, pinned: false, locked: false }
    expect(getTabIcon(termTab)).toBe('Terminal')
    expect(getTabIcon(streamTab)).toBe('ChatCircleDots')
  })

  it('getTabIcon falls back to tab.icon for unregistered type', () => {
    const tab: Tab = { id: '1', type: 'unknown', label: 't', icon: 'Fallback', hostId: 'h', data: {}, pinned: false, locked: false }
    expect(getTabIcon(tab)).toBe('Fallback')
  })

  it('overwrites existing registration', () => {
    registerTabRenderer('session', { component: mockComponent, icon: () => 'A' })
    registerTabRenderer('session', { component: mockComponent, icon: () => 'B' })
    const tab: Tab = { id: '1', type: 'session', label: 't', icon: '', hostId: 'h', data: {}, pinned: false, locked: false }
    expect(getTabIcon(tab)).toBe('B')
  })
})
