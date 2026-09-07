import { describe, it, expect, beforeEach } from 'vitest'
import { useTabStore } from './useTabStore'
import { useWorkspaceStore } from './useWorkspaceStore'
import { createTab } from '../types/tab'
import type { PaneContent } from '../types/tab'
import { getPrimaryPane } from '../lib/pane-tree'

function makeSessionTab(code: string, mode: 'terminal' | 'stream' = 'terminal') {
  return createTab({ kind: 'tmux-session', hostId: 'test-host', sessionCode: code, mode, cachedName: '', tmuxInstance: '' })
}

describe('useTabStore', () => {
  beforeEach(() => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  })

  it('addTab adds tab to tabs + tabOrder', () => {
    const tab = makeSessionTab('dev001')
    useTabStore.getState().addTab(tab)
    const state = useTabStore.getState()
    expect(state.tabs[tab.id]).toEqual(tab)
    expect(state.tabOrder).toContain(tab.id)
  })

  it('addTab sets activeTabId if none active', () => {
    const tab = makeSessionTab('dev001')
    useTabStore.getState().addTab(tab)
    expect(useTabStore.getState().activeTabId).toBe(tab.id)
  })

  it('addTab does not change activeTabId when adding second tab', () => {
    const tab1 = makeSessionTab('dev001')
    const tab2 = makeSessionTab('cld001', 'stream')
    useTabStore.getState().addTab(tab1)
    useTabStore.getState().addTab(tab2)
    expect(useTabStore.getState().activeTabId).toBe(tab1.id)
  })

  it('addTab with afterTabId inserts after specified tab', () => {
    const tab1 = makeSessionTab('dev001')
    const tab2 = makeSessionTab('dev002')
    const tab3 = makeSessionTab('dev003')
    useTabStore.getState().addTab(tab1)
    useTabStore.getState().addTab(tab2)
    useTabStore.getState().addTab(tab3, tab1.id)
    expect(useTabStore.getState().tabOrder).toEqual([tab1.id, tab3.id, tab2.id])
  })

  it('addTab with afterTabId appends when afterTabId not found', () => {
    const tab1 = makeSessionTab('dev001')
    const tab2 = makeSessionTab('dev002')
    useTabStore.getState().addTab(tab1)
    useTabStore.getState().addTab(tab2, 'nonexistent')
    expect(useTabStore.getState().tabOrder).toEqual([tab1.id, tab2.id])
  })

  it('addTab with afterTabId pointing to pinned tab inserts after pinned group', () => {
    const tab1 = makeSessionTab('dev001')
    const tab2 = makeSessionTab('dev002')
    const tab3 = makeSessionTab('dev003')
    useTabStore.getState().addTab(tab1)
    useTabStore.getState().addTab(tab2)
    useTabStore.getState().togglePin(tab1.id)
    useTabStore.getState().togglePin(tab2.id)
    // tab1 and tab2 are pinned; insert after tab1 should skip past all pinned
    useTabStore.getState().addTab(tab3, tab1.id)
    expect(useTabStore.getState().tabOrder).toEqual([tab1.id, tab2.id, tab3.id])
  })

  it('closeTab removes from tabs + tabOrder', () => {
    const tab = makeSessionTab('dev001')
    useTabStore.getState().addTab(tab)
    useTabStore.getState().closeTab(tab.id)
    expect(useTabStore.getState().tabs[tab.id]).toBeUndefined()
    expect(useTabStore.getState().tabOrder).not.toContain(tab.id)
  })

  it('closeTab on locked tab is no-op', () => {
    const tab = makeSessionTab('dev001')
    useTabStore.getState().addTab(tab)
    useTabStore.getState().toggleLock(tab.id)
    useTabStore.getState().closeTab(tab.id)
    expect(useTabStore.getState().tabs[tab.id]).toBeDefined()
    expect(useTabStore.getState().tabOrder).toContain(tab.id)
  })

  it('closeTab sets activeTabId to null when removing active tab', () => {
    const tab1 = makeSessionTab('dev001')
    const tab2 = makeSessionTab('cld001')
    useTabStore.getState().addTab(tab1)
    useTabStore.getState().addTab(tab2)
    useTabStore.getState().setActiveTab(tab1.id)
    useTabStore.getState().closeTab(tab1.id)
    expect(useTabStore.getState().activeTabId).toBeNull()
  })

  it('closeTab sets null when removing last tab', () => {
    const tab = makeSessionTab('dev001')
    useTabStore.getState().addTab(tab)
    useTabStore.getState().closeTab(tab.id)
    expect(useTabStore.getState().activeTabId).toBeNull()
  })

  it('setActiveTab updates activeTabId', () => {
    const tab1 = makeSessionTab('dev001')
    const tab2 = makeSessionTab('cld001')
    useTabStore.getState().addTab(tab1)
    useTabStore.getState().addTab(tab2)
    useTabStore.getState().setActiveTab(tab2.id)
    expect(useTabStore.getState().activeTabId).toBe(tab2.id)
  })

  it('setActiveTab ignores nonexistent id', () => {
    const tab = makeSessionTab('dev001')
    useTabStore.getState().addTab(tab)
    useTabStore.getState().setActiveTab('nonexistent')
    expect(useTabStore.getState().activeTabId).toBe(tab.id)
  })

  it('openSingletonTab returns existing tab id if content matches (singleton kinds)', () => {
    const content: PaneContent = { kind: 'dashboard' }
    const tab = createTab(content)
    useTabStore.getState().addTab(tab)
    const returnedId = useTabStore.getState().openSingletonTab(content)
    expect(returnedId).toBe(tab.id)
  })

  it('openSingletonTab always creates new tab for session (non-singleton)', () => {
    const content: PaneContent = { kind: 'tmux-session', hostId: 'test-host', sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' }
    const tab = createTab(content)
    useTabStore.getState().addTab(tab)
    const returnedId = useTabStore.getState().openSingletonTab(content)
    expect(returnedId).not.toBe(tab.id) // sessions are never singletons
  })

  it('openSingletonTab creates new tab if no match', () => {
    const content: PaneContent = { kind: 'dashboard' }
    const returnedId = useTabStore.getState().openSingletonTab(content)
    expect(useTabStore.getState().tabs[returnedId]).toBeDefined()
    expect(useTabStore.getState().tabOrder).toContain(returnedId)
  })

  it('openSingletonTab activates existing tab', () => {
    const content: PaneContent = { kind: 'dashboard' }
    const tab = createTab(content)
    useTabStore.getState().addTab(tab)
    // add another tab and make it active
    const tab2 = makeSessionTab('dev001')
    useTabStore.getState().addTab(tab2)
    useTabStore.getState().setActiveTab(tab2.id)
    expect(useTabStore.getState().activeTabId).toBe(tab2.id)
    // openSingletonTab should activate the existing dashboard tab
    useTabStore.getState().openSingletonTab(content)
    expect(useTabStore.getState().activeTabId).toBe(tab.id)
  })

  describe('openSingletonTab with opts.afterTabId', () => {
    beforeEach(() => {
      useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
    })

    const editorContent = (path: string): PaneContent =>
      ({ kind: 'editor', source: { type: 'inapp' }, filePath: path } as never)

    it('inserts new tab right after the supplied afterTabId', () => {
      const s1 = makeSessionTab('s1')
      const editor1 = createTab(editorContent('/x.ts'))
      const s2 = makeSessionTab('s2')
      useTabStore.setState({
        tabs: { [s1.id]: s1, [editor1.id]: editor1, [s2.id]: s2 },
        tabOrder: [s1.id, editor1.id, s2.id],
        activeTabId: s1.id,
      })

      const newId = useTabStore.getState().openSingletonTab(
        editorContent('/y.ts'),
        { afterTabId: editor1.id },
      )
      expect(useTabStore.getState().tabOrder).toEqual([s1.id, editor1.id, newId, s2.id])
    })

    it('without opts, appends at end (legacy behaviour)', () => {
      const s1 = makeSessionTab('s1')
      useTabStore.setState({
        tabs: { [s1.id]: s1 },
        tabOrder: [s1.id],
        activeTabId: s1.id,
      })
      const newId = useTabStore.getState().openSingletonTab(editorContent('/a.ts'))
      expect(useTabStore.getState().tabOrder).toEqual([s1.id, newId])
    })

    it('does not touch useWorkspaceStore — caller is responsible for workspace.tabs', () => {
      const s1 = makeSessionTab('s1')
      useTabStore.setState({
        tabs: { [s1.id]: s1 },
        tabOrder: [s1.id],
        activeTabId: s1.id,
      })
      useWorkspaceStore.setState({
        workspaces: [{
          id: 'w', name: 'W', tabs: [s1.id], activeTabId: s1.id, moduleConfig: {},
        }],
        activeWorkspaceId: 'w',
      })
      const newId = useTabStore.getState().openSingletonTab(
        editorContent('/y.ts'),
        { afterTabId: s1.id },
      )
      const ws = useWorkspaceStore.getState().workspaces[0]
      expect(ws.tabs).toEqual([s1.id])
      expect(useTabStore.getState().tabs[newId]).toBeDefined()
    })
  })

  it('setViewMode updates pane mode', () => {
    const tab = makeSessionTab('dev001')
    useTabStore.getState().addTab(tab)
    const paneId = tab.layout.type === 'leaf' ? tab.layout.pane.id : ''
    useTabStore.getState().setViewMode(tab.id, paneId, 'stream')
    const updated = useTabStore.getState().tabs[tab.id]
    const content = updated.layout.type === 'leaf' ? updated.layout.pane.content : undefined
    expect(content?.kind === 'tmux-session' && content.mode).toBe('stream')
  })

  it('setViewMode is no-op for nonexistent tab', () => {
    useTabStore.getState().setViewMode('nonexistent', 'pane1', 'stream')
    expect(Object.keys(useTabStore.getState().tabs)).toHaveLength(0)
  })

  it('reorderTabs updates tabOrder', () => {
    const tab1 = makeSessionTab('a')
    const tab2 = makeSessionTab('b')
    const tab3 = makeSessionTab('c')
    useTabStore.getState().addTab(tab1)
    useTabStore.getState().addTab(tab2)
    useTabStore.getState().addTab(tab3)
    useTabStore.getState().reorderTabs([tab3.id, tab1.id, tab2.id])
    expect(useTabStore.getState().tabOrder).toEqual([tab3.id, tab1.id, tab2.id])
  })

  it('closeTab is no-op for nonexistent id', () => {
    const tab = makeSessionTab('dev001')
    useTabStore.getState().addTab(tab)
    useTabStore.getState().closeTab('nonexistent')
    expect(useTabStore.getState().tabOrder).toHaveLength(1)
  })

  describe('setPaneContent', () => {
    it('updates pane content by tabId and paneId', () => {
      const tab = makeSessionTab('dev001')
      useTabStore.getState().addTab(tab)
      const paneId = tab.layout.type === 'leaf' ? tab.layout.pane.id : ''
      const newContent: PaneContent = { kind: 'dashboard' }
      useTabStore.getState().setPaneContent(tab.id, paneId, newContent)
      const updated = useTabStore.getState().tabs[tab.id]
      expect(updated.layout.type).toBe('leaf')
      if (updated.layout.type === 'leaf') {
        expect(updated.layout.pane.content).toEqual({ kind: 'dashboard' })
      }
    })

    it('is no-op for nonexistent tab', () => {
      useTabStore.getState().setPaneContent('nonexistent', 'pane1', { kind: 'dashboard' })
      expect(Object.keys(useTabStore.getState().tabs)).toHaveLength(0)
    })

    it('is no-op for nonexistent pane (layout unchanged)', () => {
      const tab = makeSessionTab('dev001')
      useTabStore.getState().addTab(tab)
      const before = useTabStore.getState().tabs[tab.id].layout
      useTabStore.getState().setPaneContent(tab.id, 'nonexistent-pane', { kind: 'dashboard' })
      const after = useTabStore.getState().tabs[tab.id].layout
      // Layout should be structurally the same (content unchanged)
      if (before.type === 'leaf' && after.type === 'leaf') {
        expect(after.pane.content).toEqual(before.pane.content)
      }
    })
  })

  describe('remountPane', () => {
    it('replaces the target pane id in place and returns the new id', () => {
      const content: PaneContent = { kind: 'image-preview', source: { type: 'inapp' }, filePath: '/buffer/a.png' }
      const tab = createTab(content)
      useTabStore.getState().addTab(tab)
      const oldId = tab.layout.type === 'leaf' ? tab.layout.pane.id : ''

      const newId = useTabStore.getState().remountPane(tab.id, oldId)
      expect(newId).not.toBeNull()
      expect(newId).not.toBe(oldId)
      const updated = useTabStore.getState().tabs[tab.id]
      expect(updated.layout.type).toBe('leaf')
      if (updated.layout.type === 'leaf') {
        expect(updated.layout.pane.id).toBe(newId)
        expect(updated.layout.pane.content).toEqual(content)
      }
    })

    it('only touches the target leaf in a split (sibling preserved)', () => {
      const content: PaneContent = { kind: 'image-preview', source: { type: 'inapp' }, filePath: '/buffer/a.png' }
      const tab = createTab(content)
      useTabStore.getState().addTab(tab)
      const firstId = tab.layout.type === 'leaf' ? tab.layout.pane.id : ''
      useTabStore.getState().splitPane(tab.id, firstId, 'h', { kind: 'dashboard' })
      const split = useTabStore.getState().tabs[tab.id].layout
      const siblingId = split.type === 'split' && split.children[1].type === 'leaf' ? split.children[1].pane.id : ''

      const newId = useTabStore.getState().remountPane(tab.id, firstId)
      const after = useTabStore.getState().tabs[tab.id].layout
      if (after.type === 'split') {
        expect(after.children[0].type === 'leaf' && after.children[0].pane.id).toBe(newId)
        expect(after.children[1].type === 'leaf' && after.children[1].pane.id).toBe(siblingId)
      }
    })

    it('returns null for an unknown pane', () => {
      const tab = createTab({ kind: 'dashboard' })
      useTabStore.getState().addTab(tab)
      expect(useTabStore.getState().remountPane(tab.id, 'nope')).toBeNull()
    })
  })

  describe('renameEditorPanes', () => {
    it('renames matching editor panes in a split layout', () => {
      const content: PaneContent = { kind: 'editor', source: { type: 'inapp' }, filePath: '/notes/a.md' }
      const tab = createTab(content)
      useTabStore.getState().addTab(tab)
      const paneId = tab.layout.type === 'leaf' ? tab.layout.pane.id : ''

      useTabStore.getState().splitPane(tab.id, paneId, 'h', content)
      useTabStore.getState().renameEditorPanes({ type: 'inapp' }, '/notes/a.md', '/notes/b.md')

      const updated = useTabStore.getState().tabs[tab.id]
      expect(updated.layout.type).toBe('split')
      if (updated.layout.type === 'split') {
        const filePaths = updated.layout.children.map((child) =>
          child.type === 'leaf' && child.pane.content.kind === 'editor'
            ? child.pane.content.filePath
            : null,
        )
        expect(filePaths).toEqual(['/notes/b.md', '/notes/b.md'])
      }
    })

    it('renames matching image-preview and pdf-preview panes', () => {
      const imgTab = createTab({ kind: 'image-preview', source: { type: 'inapp' }, filePath: '/buffer/p.png' })
      const pdfTab = createTab({ kind: 'pdf-preview', source: { type: 'inapp' }, filePath: '/buffer/p.png' })
      useTabStore.getState().addTab(imgTab)
      useTabStore.getState().addTab(pdfTab)

      useTabStore.getState().renameEditorPanes({ type: 'inapp' }, '/buffer/p.png', '/buffer/q.png')

      const img = useTabStore.getState().tabs[imgTab.id].layout
      const pdf = useTabStore.getState().tabs[pdfTab.id].layout
      expect(img.type === 'leaf' && img.pane.content.kind === 'image-preview' && img.pane.content.filePath).toBe('/buffer/q.png')
      expect(pdf.type === 'leaf' && pdf.pane.content.kind === 'pdf-preview' && pdf.pane.content.filePath).toBe('/buffer/q.png')
    })

    it('preserves untitled handling for editor panes (preview broadening does not regress)', () => {
      const untitled = { name: 'Untitled', suggestedExtension: '.md' as const, hasBeenRenamed: false }
      const editorTab = createTab({ kind: 'editor', source: { type: 'inapp' }, filePath: '/buffer/u.md', untitled })
      useTabStore.getState().addTab(editorTab)

      // No untitled option → the field is cleared (existing contract).
      useTabStore.getState().renameEditorPanes({ type: 'inapp' }, '/buffer/u.md', '/buffer/named.md')
      const after = useTabStore.getState().tabs[editorTab.id].layout
      expect(after.type).toBe('leaf')
      if (after.type === 'leaf' && after.pane.content.kind === 'editor') {
        expect(after.pane.content.filePath).toBe('/buffer/named.md')
        expect(after.pane.content.untitled).toBeUndefined()
      }
    })
  })

  describe('updateSessionCache', () => {
    it('updates cachedName for matching session tab', () => {
      const tab = makeSessionTab('dev001', 'terminal')
      useTabStore.getState().addTab(tab)
      const tabId = useTabStore.getState().tabOrder[0]

      useTabStore.getState().updateSessionCache('test-host', 'dev001', 'renamed-session', '')

      const content = getPrimaryPane(useTabStore.getState().tabs[tabId].layout).content
      expect(content.kind).toBe('tmux-session')
      if (content.kind === 'tmux-session') {
        expect(content.cachedName).toBe('renamed-session')
      }
    })

    it('does not update tab with different sessionCode', () => {
      const tab = makeSessionTab('dev001')
      useTabStore.getState().addTab(tab)
      const tabId = useTabStore.getState().tabOrder[0]

      useTabStore.getState().updateSessionCache('test-host', 'dev999', 'renamed', '')

      const content = getPrimaryPane(useTabStore.getState().tabs[tabId].layout).content
      expect(content.kind).toBe('tmux-session')
      if (content.kind === 'tmux-session') {
        expect(content.cachedName).toBe('')
      }
    })

    it('does not update tab with different hostId', () => {
      const tab = makeSessionTab('dev001')
      useTabStore.getState().addTab(tab)
      const tabId = useTabStore.getState().tabOrder[0]

      useTabStore.getState().updateSessionCache('other-host', 'dev001', 'renamed', '')

      const content = getPrimaryPane(useTabStore.getState().tabs[tabId].layout).content
      expect(content.kind).toBe('tmux-session')
      if (content.kind === 'tmux-session') {
        expect(content.cachedName).toBe('')
      }
    })

    it('is no-op when cachedName is already the same', () => {
      const tab = makeSessionTab('dev001')
      useTabStore.getState().addTab(tab)
      const tabId = useTabStore.getState().tabOrder[0]
      const before = useTabStore.getState().tabs[tabId]

      useTabStore.getState().updateSessionCache('test-host', 'dev001', '', '')

      const after = useTabStore.getState().tabs[tabId]
      expect(after).toBe(before) // same reference — no update
    })

    it('updates multiple matching tabs', () => {
      const tab1 = makeSessionTab('dev001')
      const tab2 = makeSessionTab('dev001', 'stream')
      useTabStore.getState().addTab(tab1)
      useTabStore.getState().addTab(tab2)

      useTabStore.getState().updateSessionCache('test-host', 'dev001', 'new-name', '')

      for (const tabId of useTabStore.getState().tabOrder) {
        const content = getPrimaryPane(useTabStore.getState().tabs[tabId].layout).content
        expect(content.kind).toBe('tmux-session')
        if (content.kind === 'tmux-session') {
          expect(content.cachedName).toBe('new-name')
        }
      }
    })
  })

  describe('visitHistory', () => {
    beforeEach(() => {
      useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    })

    it('records previous tab when switching', () => {
      const tab1 = makeSessionTab('dev001')
      const tab2 = makeSessionTab('dev002')
      useTabStore.getState().addTab(tab1)
      useTabStore.getState().addTab(tab2)
      // tab1 is active (first tab added), switch to tab2
      useTabStore.getState().setActiveTab(tab2.id)
      expect(useTabStore.getState().visitHistory).toContain(tab1.id)
    })

    it('does not record when switching to same tab', () => {
      const tab1 = makeSessionTab('dev001')
      useTabStore.getState().addTab(tab1)
      useTabStore.getState().setActiveTab(tab1.id)
      // switching to the same tab should not push to history
      useTabStore.getState().setActiveTab(tab1.id)
      expect(useTabStore.getState().visitHistory).toHaveLength(0)
    })

    it('does not record null activeTabId', () => {
      const tab1 = makeSessionTab('dev001')
      useTabStore.getState().addTab(tab1)
      // set null active, then switch to tab1 — null should not be recorded
      useTabStore.setState({ activeTabId: null })
      useTabStore.getState().setActiveTab(tab1.id)
      expect(useTabStore.getState().visitHistory).toHaveLength(0)
    })

    it('closeTab removes closed tab id from visitHistory', () => {
      const tab1 = makeSessionTab('dev001')
      const tab2 = makeSessionTab('dev002')
      const tab3 = makeSessionTab('dev003')
      useTabStore.getState().addTab(tab1)
      useTabStore.getState().addTab(tab2)
      useTabStore.getState().addTab(tab3)
      // Build up history with tab1 in it
      useTabStore.getState().setActiveTab(tab2.id) // history: [tab1]
      useTabStore.getState().setActiveTab(tab3.id) // history: [tab1, tab2]
      // Close tab1 (not active) — should remove tab1 from history
      useTabStore.getState().closeTab(tab1.id)
      expect(useTabStore.getState().visitHistory).not.toContain(tab1.id)
    })
  })

})
