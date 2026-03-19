import { describe, it, expect, beforeEach } from 'vitest'
import { useWorkspaceStore } from './useWorkspaceStore'

describe('useWorkspaceStore', () => {
  beforeEach(() => {
    useWorkspaceStore.getState().reset()
  })

  it('initializes with a default workspace', () => {
    const state = useWorkspaceStore.getState()
    expect(state.workspaces.length).toBe(1)
    expect(state.workspaces[0].name).toBe('Default')
    expect(state.activeWorkspaceId).toBe(state.workspaces[0].id)
  })

  it('adds a tab to workspace', () => {
    const wsId = useWorkspaceStore.getState().workspaces[0].id
    useWorkspaceStore.getState().addTabToWorkspace(wsId, 'tab-1')
    const ws = useWorkspaceStore.getState().workspaces[0]
    expect(ws.tabs).toContain('tab-1')
  })

  it('removes a tab from workspace', () => {
    const wsId = useWorkspaceStore.getState().workspaces[0].id
    useWorkspaceStore.getState().addTabToWorkspace(wsId, 'tab-1')
    useWorkspaceStore.getState().removeTabFromWorkspace(wsId, 'tab-1')
    const ws = useWorkspaceStore.getState().workspaces[0]
    expect(ws.tabs).not.toContain('tab-1')
  })

  it('switches active workspace', () => {
    const ws2 = useWorkspaceStore.getState().addWorkspace('Project B')
    useWorkspaceStore.getState().setActiveWorkspace(ws2.id)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws2.id)
  })

  it('adds a workspace', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('New WS')
    expect(ws.name).toBe('New WS')
    expect(useWorkspaceStore.getState().workspaces).toHaveLength(2)
  })

  it('removes a workspace', () => {
    const ws2 = useWorkspaceStore.getState().addWorkspace('To Remove')
    useWorkspaceStore.getState().removeWorkspace(ws2.id)
    expect(useWorkspaceStore.getState().workspaces).toHaveLength(1)
  })

  it('cannot remove the last workspace', () => {
    const wsId = useWorkspaceStore.getState().workspaces[0].id
    useWorkspaceStore.getState().removeWorkspace(wsId)
    expect(useWorkspaceStore.getState().workspaces).toHaveLength(1)
  })

  it('finds workspace containing a tab', () => {
    const wsId = useWorkspaceStore.getState().workspaces[0].id
    useWorkspaceStore.getState().addTabToWorkspace(wsId, 'tab-1')
    expect(useWorkspaceStore.getState().findWorkspaceByTab('tab-1')?.id).toBe(wsId)
    expect(useWorkspaceStore.getState().findWorkspaceByTab('tab-unknown')).toBeNull()
  })

  it('sets workspace active tab', () => {
    const wsId = useWorkspaceStore.getState().workspaces[0].id
    useWorkspaceStore.getState().addTabToWorkspace(wsId, 'tab-1')
    useWorkspaceStore.getState().setWorkspaceActiveTab(wsId, 'tab-1')
    const ws = useWorkspaceStore.getState().workspaces.find(w => w.id === wsId)!
    expect(ws.activeTabId).toBe('tab-1')
  })

  it('does not add duplicate tab to workspace', () => {
    const wsId = useWorkspaceStore.getState().workspaces[0].id
    useWorkspaceStore.getState().addTabToWorkspace(wsId, 'tab-1')
    useWorkspaceStore.getState().addTabToWorkspace(wsId, 'tab-1')
    const ws = useWorkspaceStore.getState().workspaces[0]
    expect(ws.tabs).toEqual(['tab-1'])
  })

  it('switches activeWorkspaceId when removing active workspace', () => {
    const ws2 = useWorkspaceStore.getState().addWorkspace('WS2')
    useWorkspaceStore.getState().setActiveWorkspace(ws2.id)
    useWorkspaceStore.getState().removeWorkspace(ws2.id)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(useWorkspaceStore.getState().workspaces[0].id)
  })
})
