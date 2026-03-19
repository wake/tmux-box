import { describe, it, expect } from 'vitest'
import { createTab, createWorkspace, isStandaloneTab } from './tab'

describe('createTab', () => {
  it('creates a terminal tab with generated id', () => {
    const tab = createTab({ type: 'terminal', label: 'dev-server', hostId: 'mlab', sessionName: 'dev-server' })
    expect(tab.id).toBeTruthy()
    expect(tab.type).toBe('terminal')
    expect(tab.label).toBe('dev-server')
    expect(tab.hostId).toBe('mlab')
    expect(tab.sessionName).toBe('dev-server')
    expect(tab.icon).toBe('Terminal')
  })

  it('creates a stream tab', () => {
    const tab = createTab({ type: 'stream', label: 'claude-code', hostId: 'mlab', sessionName: 'claude-code' })
    expect(tab.type).toBe('stream')
    expect(tab.icon).toBe('ChatCircleDots')
  })

  it('creates an editor tab', () => {
    const tab = createTab({ type: 'editor', label: 'App.tsx', hostId: 'mlab', filePath: '/src/App.tsx' })
    expect(tab.type).toBe('editor')
    expect(tab.filePath).toBe('/src/App.tsx')
    expect(tab.isDirty).toBe(false)
    expect(tab.icon).toBe('File')
  })
})

describe('createWorkspace', () => {
  it('creates a workspace with defaults', () => {
    const ws = createWorkspace({ name: 'My Project' })
    expect(ws.id).toBeTruthy()
    expect(ws.name).toBe('My Project')
    expect(ws.color).toBeTruthy()
    expect(ws.tabs).toEqual([])
    expect(ws.directories).toEqual([])
    expect(ws.activeTabId).toBeNull()
    expect(ws.sidebarState).toBeDefined()
    expect(ws.sidebarState.zones).toBeDefined()
    expect(ws.sidebarState.zones['left-outer']).toBeDefined()
    expect(ws.sidebarState.zones['left-inner']).toBeDefined()
    expect(ws.sidebarState.zones['right-inner']).toBeDefined()
    expect(ws.sidebarState.zones['right-outer']).toBeDefined()
  })
})

describe('isStandaloneTab', () => {
  it('returns true when tab is not in any workspace', () => {
    const tab = createTab({ type: 'terminal', label: 'misc', hostId: 'mlab' })
    const workspaces = [createWorkspace({ name: 'WS1' })]
    expect(isStandaloneTab(tab.id, workspaces)).toBe(true)
  })

  it('returns false when tab is in a workspace', () => {
    const tab = createTab({ type: 'terminal', label: 'dev', hostId: 'mlab' })
    const ws = createWorkspace({ name: 'WS1' })
    ws.tabs = [tab.id]
    expect(isStandaloneTab(tab.id, [ws])).toBe(false)
  })
})
