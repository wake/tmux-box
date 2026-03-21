import { describe, it, expect } from 'vitest'
import { createSessionTab, createEditorTab, createTab, createWorkspace, isStandaloneTab } from './tab'

describe('createSessionTab', () => {
  it('creates a session tab with terminal viewMode', () => {
    const tab = createSessionTab({ label: 'dev-server', hostId: 'mlab', sessionName: 'dev-server' })
    expect(tab.id).toBeTruthy()
    expect(tab.type).toBe('session')
    expect(tab.label).toBe('dev-server')
    expect(tab.hostId).toBe('mlab')
    expect(tab.viewMode).toBe('terminal')
    expect(tab.data.sessionName).toBe('dev-server')
    expect(tab.icon).toBe('TerminalWindow')
  })

  it('creates a session tab with stream viewMode', () => {
    const tab = createSessionTab({ label: 'claude', hostId: 'mlab', sessionName: 'claude', viewMode: 'stream' })
    expect(tab.type).toBe('session')
    expect(tab.viewMode).toBe('stream')
    expect(tab.data.sessionName).toBe('claude')
  })
})

describe('createEditorTab', () => {
  it('creates an editor tab', () => {
    const tab = createEditorTab({ label: 'App.tsx', hostId: 'mlab', filePath: '/src/App.tsx' })
    expect(tab.type).toBe('editor')
    expect(tab.data.filePath).toBe('/src/App.tsx')
    expect(tab.data.isDirty).toBe(false)
    expect(tab.icon).toBe('File')
  })
})

describe('createTab (generic)', () => {
  it('creates a tab with arbitrary type and data', () => {
    const tab = createTab({ type: 'monitoring', label: 'Dashboard', hostId: 'mlab', data: { url: '/metrics' } })
    expect(tab.type).toBe('monitoring')
    expect(tab.data.url).toBe('/metrics')
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
    expect(ws.sidebarState.zones['left-outer']).toBeDefined()
  })
})

describe('isStandaloneTab', () => {
  it('returns true when tab is not in any workspace', () => {
    const tab = createSessionTab({ label: 'misc', hostId: 'mlab', sessionName: 'misc' })
    const workspaces = [createWorkspace({ name: 'WS1' })]
    expect(isStandaloneTab(tab.id, workspaces)).toBe(true)
  })

  it('returns false when tab is in a workspace', () => {
    const tab = createSessionTab({ label: 'dev', hostId: 'mlab', sessionName: 'dev' })
    const ws = createWorkspace({ name: 'WS1' })
    ws.tabs = [tab.id]
    expect(isStandaloneTab(tab.id, [ws])).toBe(false)
  })
})

describe('pinned / locked defaults', () => {
  it('createSessionTab defaults pinned=false, locked=false', () => {
    const tab = createSessionTab({ label: 'x', hostId: 'local', sessionName: 'x' })
    expect(tab.pinned).toBe(false)
    expect(tab.locked).toBe(false)
  })

  it('createEditorTab defaults pinned=false, locked=false', () => {
    const tab = createEditorTab({ label: 'x', hostId: 'local', filePath: '/tmp/x' })
    expect(tab.pinned).toBe(false)
    expect(tab.locked).toBe(false)
  })

  it('createTab defaults pinned=false, locked=false', () => {
    const tab = createTab({ type: 'custom', label: 'x', hostId: 'local' })
    expect(tab.pinned).toBe(false)
    expect(tab.locked).toBe(false)
  })
})
