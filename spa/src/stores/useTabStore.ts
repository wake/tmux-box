import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import type { Tab, PaneContent, PaneLayout, TerminatedReason, LayoutPattern, PaneRebuildRecord, RebuildPatch, TmuxSessionContent } from '../types/tab'
import type { FileSource } from '../types/fs'
import { createTab } from '../types/tab'
import { getPrimaryPane, findPane, updatePaneInLayout, splitAtPane, removePane, applyLayoutPattern, remountLeaf } from '../lib/pane-tree'
import { contentMatches, isFilePaneContent } from '../lib/pane-utils'
import { bindingMatchesLegacy, generationMatchesLegacy } from '../lib/rebuild/binding'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'
import type { UntitledDocumentState } from '../types/tab'

// --- Persist migration helpers ---
// These functions handle legacy persisted data whose shape no longer matches
// current TypeScript types, so `any` casts are unavoidable.

function migrateLayout(layout: PaneLayout): PaneLayout {
  if (layout.type === 'leaf') {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const content = layout.pane.content as any
    if (content.kind === 'session') {
      return {
        ...layout,
        pane: { ...layout.pane, content: { ...content, kind: 'tmux-session' } },
      }
    }
    return layout
  }
  return { ...layout, children: layout.children.map(migrateLayout) }
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function migrateTabStore(state: any, version: number): any {
  if (version < 2) {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const tabs: Record<string, any> = {}
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    for (const [id, tab] of Object.entries(state.tabs as Record<string, any>)) {
      tabs[id] = { ...tab, layout: migrateLayout(tab.layout) }
    }
    return { ...state, tabs }
  }
  return state
}

// --- Terminated marking helpers ---

/**
 * Mark panes bound to (hostId, sessionCode) as terminated.
 *
 * `expectedTmuxInstance` narrows the match to one tmux generation (spec §4.5):
 * a pane that has already re-bound to a newer generation of the same reused
 * session code is left alone. A pane whose recorded instance is `''` (legacy
 * pane, or a host whose generation is unknown) is matched by the old
 * host+code rule so nothing regresses. `undefined` = no generation predicate.
 */
function markPanesInLayout(
  layout: PaneLayout,
  hostId: string,
  sessionCode: string,
  reason: TerminatedReason,
  expectedTmuxInstance?: string,
): PaneLayout {
  if (layout.type === 'leaf') {
    const c = layout.pane.content
    const generationMatches = expectedTmuxInstance === undefined
      || c.kind !== 'tmux-session'
      || generationMatchesLegacy(c.tmuxInstance, expectedTmuxInstance)
    if (c.kind === 'tmux-session' && c.hostId === hostId && c.sessionCode === sessionCode && generationMatches && !c.terminated) {
      return { ...layout, pane: { ...layout.pane, content: { ...c, terminated: reason } } }
    }
    return layout
  }
  const children = layout.children.map((child) => markPanesInLayout(child, hostId, sessionCode, reason, expectedTmuxInstance))
  return children.some((c, i) => c !== layout.children[i]) ? { ...layout, children } : layout
}

/**
 * Stamp the tmux generation onto panes bound to (hostId, sessionCode) that
 * carry none. Never overwrites a generation a pane already has — that would be
 * assuming a binding rather than observing it.
 */
function adoptInstanceInLayout(layout: PaneLayout, hostId: string, sessionCode: string, tmuxInstance: string): PaneLayout {
  if (layout.type === 'leaf') {
    const c = layout.pane.content
    if (c.kind === 'tmux-session' && c.hostId === hostId && c.sessionCode === sessionCode && c.tmuxInstance === '') {
      return { ...layout, pane: { ...layout.pane, content: { ...c, tmuxInstance } } }
    }
    return layout
  }
  const children = layout.children.map((child) => adoptInstanceInLayout(child, hostId, sessionCode, tmuxInstance))
  return children.some((c, i) => c !== layout.children[i]) ? { ...layout, children } : layout
}

// --- Rebuild record helpers (spec §4.1 / §4.4 / §4.5) ---

/**
 * Does this pane belong to the binding a writer is addressing?
 *
 * The legacy-compatible comparison (`binding.ts`): host and code exact, and a
 * pane whose recorded instance is `''` — a legacy pane, or one on a host whose
 * generation is unknown — answers to any expected instance, which is the same
 * rule `markPanesInLayout` uses.
 */
function sessionBindingMatches(
  c: PaneContent,
  hostId: string,
  sessionCode: string,
  expectedTmuxInstance: string,
): c is TmuxSessionContent {
  return c.kind === 'tmux-session'
    && bindingMatchesLegacy(c, { hostId, sessionCode, tmuxInstance: expectedTmuxInstance })
}

/** As above, minus stream-mode panes, which are out of scope for rebuild. */
function rebuildBindingMatches(
  c: PaneContent,
  hostId: string,
  sessionCode: string,
  expectedTmuxInstance: string,
): c is TmuxSessionContent {
  return sessionBindingMatches(c, hostId, sessionCode, expectedTmuxInstance) && c.mode === 'terminal'
}

/**
 * The final eligibility check for a SESSION-scoped record write, per pane.
 *
 * A terminated pane is excluded. The readers that trigger these writes — the
 * cwd probe, the agent-group envelope — decide whether to ask while every pane
 * on the binding is still alive, but the answer arrives later, and by then one
 * pane may have died while a sibling on the same reused code is still live. The
 * answer then describes the sibling's session, and writing it into the dead
 * pane's record would put a stranger's directory (or agent) into the rebuild
 * that pane offers. So the check that matters is made HERE, against each pane,
 * in the same synchronous `set` that writes.
 *
 * Pane-scoped writes (`setPaneRebuildForPane`) deliberately do NOT use this: a
 * terminated pane is exactly where the user edits the cwd before rebuilding it.
 */
function acceptsSessionScopedWrite(
  c: PaneContent,
  hostId: string,
  sessionCode: string,
  expectedTmuxInstance: string,
): c is TmuxSessionContent {
  return rebuildBindingMatches(c, hostId, sessionCode, expectedTmuxInstance) && !c.terminated
}

/**
 * Rewrite every tmux-session leaf the predicate selects. `update` returning
 * its argument unchanged keeps the layout reference stable, so a no-op write
 * never re-renders.
 */
function mapTmuxPanesInLayout(
  layout: PaneLayout,
  match: (c: PaneContent) => c is TmuxSessionContent,
  update: (c: TmuxSessionContent) => TmuxSessionContent,
): PaneLayout {
  if (layout.type === 'leaf') {
    const c = layout.pane.content
    if (!match(c)) return layout
    const next = update(c)
    if (next === c) return layout
    return { ...layout, pane: { ...layout.pane, content: next } }
  }
  const children = layout.children.map((child) => mapTmuxPanesInLayout(child, match, update))
  return children.some((c, i) => c !== layout.children[i]) ? { ...layout, children } : layout
}

/**
 * Whether a write landing `next` invalidates an override written while the
 * record held `prev` (spec §4.3).
 *
 * The rule is a product policy, not a proof. It is neither necessary nor
 * sufficient — `cld-yolo -c` stays valid across an identity change and is
 * discarded anyway — but the one hazard that silently does the WRONG thing is
 * a verbatim command carrying a dead session id, and that is exactly an
 * identity change. A discarded override is visible and retypable; a silently
 * stale one is not.
 *
 * No previous agent means no identity was named, so nothing was invalidated.
 */
function identityInvalidates(
  prev: PaneRebuildRecord['agent'],
  next: PaneRebuildRecord['agent'],
): boolean {
  if (!prev) return false
  return prev.type !== next?.type || (prev.sessionId ?? '') !== (next?.sessionId ?? '')
}

/**
 * Apply one `RebuildPatch` to a pane, creating the record on first write.
 *
 * Returns the same content object when the patch changes nothing (a probe
 * against a cwd that is already known, an edit that retypes the same value).
 * Every write that does land re-stamps `capturedAt`, which is what makes the
 * batch view's "latest edit wins" resolution meaningful.
 */
function applyRebuildPatch(c: TmuxSessionContent, patch: RebuildPatch): TmuxSessionContent {
  const prev: PaneRebuildRecord = c.rebuild ?? {
    sessionName: c.cachedName,
    tmuxInstance: c.tmuxInstance,
    capturedAt: 0,
  }
  const now = Date.now()
  let next: PaneRebuildRecord

  switch (patch.kind) {
    case 'agent-group': {
      const { record } = patch
      // One unit: everything the agent group owns is replaced together, so a
      // payload without cwd clears cwd instead of leaving the previous agent's
      // directory beside a new session id. `unverified` is cleared too — it
      // only ever meant "this agent group disagrees with the daemon", and this
      // is a fresh, authoritative agent group.
      //
      // `resumeCommandOverride` is NOT in the unit. It is a user edit, and it
      // is discarded only when the identity it was written against changed
      // (spec §4.3) — an idle SessionStart re-emit carrying the same id must
      // keep it. A record that held no agent has no identity to have changed,
      // so nothing invalidated the edit and it stays; that is the same rule
      // the backfill's fill mode obeys.
      next = {
        sessionName: prev.sessionName,
        tmuxInstance: record.tmuxInstance || prev.tmuxInstance,
        cwd: record.cwd,
        cwdSource: record.cwd === undefined ? undefined : (record.cwdSource ?? 'agent-session-start'),
        agent: record.agent,
        ...(identityInvalidates(prev.agent, record.agent)
          ? {}
          : { resumeCommandOverride: prev.resumeCommandOverride }),
        capturedAt: now,
      }
      break
    }
    case 'agent-backfill': {
      // The daemon's ownership answer, applied under FOUR ORDERED modes: the
      // first match wins (spec §5.5). The order is the whole policy — an
      // unordered table let one state match two rows.
      const { record } = patch

      // Mode 1 — FILL. Nothing was known, so take the answer, but never step on
      // provenance that outranks a process-tree inference: a cwd the user typed
      // or a SessionStart reported stays, and only a probe's cwd is upgraded.
      if (!prev.agent) {
        const takesCwd = record.cwd !== undefined && (prev.cwd === undefined || prev.cwdSource === 'pane-probe')
        next = {
          ...prev,
          tmuxInstance: record.tmuxInstance || prev.tmuxInstance,
          agent: record.agent,
          ...(takesCwd ? { cwd: record.cwd, cwdSource: 'agent-backfill' as const } : {}),
          // `resumeCommandOverride` rides through untouched: nothing was
          // invalidated, because the record held no identity to invalidate.
          capturedAt: now,
        }
        break
      }

      const identityMatches =
        prev.agent.type === record.agent.type &&
        (prev.agent.sessionId ?? '') === (record.agent.sessionId ?? '')

      // Mode 2 — REPLACE. The record is flagged and the answer names someone
      // else, so the whole group goes as one unit exactly like `agent-group`:
      // correcting the agent while leaving the previous agent's cwd and command
      // attached is the cross-identity mixture whole-group writes exist to
      // prevent. A `cwdSource: 'user'` cwd is the one thing kept — the override
      // is dropped by omission, since the identity it named is the one this
      // correction just replaced (spec §4.3).
      if (prev.unverified && !identityMatches) {
        const keepsUserCwd = prev.cwd !== undefined && prev.cwdSource === 'user'
        next = {
          sessionName: prev.sessionName,
          tmuxInstance: record.tmuxInstance || prev.tmuxInstance,
          cwd: keepsUserCwd ? prev.cwd : record.cwd,
          cwdSource: keepsUserCwd ? 'user' : record.cwd === undefined ? undefined : 'agent-backfill',
          agent: record.agent,
          capturedAt: now,
        }
        break
      }

      // Mode 3 — CONFIRM. The daemon agrees, which is positive evidence the
      // record is right. This is what makes the probe terminate: without it an
      // agreeing answer would leave the pane eligible and re-asking forever,
      // since the projection's TopFrame can legitimately differ indefinitely.
      if (prev.unverified) {
        next = { ...prev, unverified: undefined, capturedAt: now }
        break
      }

      // Mode 4 — NO-OP. An agent is present and verified: "有了就跳過".
      return c
    }
    case 'field': {
      // A cwd edit also stamps `cwdSource: 'user'`, so retyping the directory a
      // probe already found is a CONFIRMATION, not a no-op: the value is
      // unchanged but the provenance is not, and the agent backfill's fill mode
      // reads that provenance to decide whether it may overwrite. Once the
      // source is already 'user' there is nothing left to change, and the early
      // return still covers the override and `sessionName` unconditionally.
      const promotesCwdSource = patch.field === 'cwd' && prev.cwdSource !== 'user'
      if (prev[patch.field] === patch.value && !promotesCwdSource) return c
      // Spelled out per field so the record keeps its exact shape. An emptied
      // override is DROPPED rather than stored as '': clearing the row is how
      // the user goes back to the agent's template, so the record has to end
      // up in the state it was in before they typed anything.
      next = patch.field === 'cwd' ? { ...prev, cwd: patch.value, cwdSource: 'user', capturedAt: now }
        : patch.field === 'resumeCommandOverride'
          ? { ...prev, resumeCommandOverride: patch.value || undefined, capturedAt: now }
          : { ...prev, sessionName: patch.value, capturedAt: now }
      break
    }
    case 'probe-cwd': {
      if (prev.cwd) return c   // agent provenance (or an edit) already won
      next = { ...prev, cwd: patch.cwd, cwdSource: 'pane-probe', capturedAt: now }
      break
    }
    case 'unverified': {
      if (prev.unverified === patch.unverified) return c
      next = { ...prev, unverified: patch.unverified, capturedAt: now }
      break
    }
  }

  return { ...c, rebuild: next }
}

/**
 * Refresh the pane's display name and, when it has a record, the name the
 * rebuild would recreate the session under.
 */
function applySessionName(c: TmuxSessionContent, cachedName: string): TmuxSessionContent {
  const nameChanged = c.cachedName !== cachedName
  const recordChanged = c.rebuild !== undefined && c.rebuild.sessionName !== cachedName
  if (!nameChanged && !recordChanged) return c
  return {
    ...c,
    cachedName,
    rebuild: recordChanged ? { ...c.rebuild!, sessionName: cachedName } : c.rebuild,
  }
}

function markHostPanesInLayout(layout: PaneLayout, hostId: string, reason: TerminatedReason): PaneLayout {
  if (layout.type === 'leaf') {
    const c = layout.pane.content
    if (c.kind === 'tmux-session' && c.hostId === hostId && !c.terminated) {
      return { ...layout, pane: { ...layout.pane, content: { ...c, terminated: reason } } }
    }
    return layout
  }
  const children = layout.children.map((child) => markHostPanesInLayout(child, hostId, reason))
  return children.some((c, i) => c !== layout.children[i]) ? { ...layout, children } : layout
}

function sourceMatches(a: FileSource, b: FileSource): boolean {
  if (a.type !== b.type) return false
  if (a.type === 'daemon' && b.type === 'daemon') {
    return a.hostId === b.hostId
  }
  return true
}

// Despite the name, this now rewrites filePath for ALL file-preview pane kinds
// (editor + image-preview + pdf-preview), so renaming a png/pdf open in a
// preview pane doesn't strand it on the old path. The exported
// `renameEditorPanes` name is kept to avoid rippling through call sites.
function renameEditorPanesInLayout(
  layout: PaneLayout,
  source: FileSource,
  oldPath: string,
  newPath: string,
  options?: { untitled?: UntitledDocumentState },
): PaneLayout {
  if (layout.type === 'leaf') {
    const content = layout.pane.content
    if (isFilePaneContent(content) && content.filePath === oldPath && sourceMatches(content.source, source)) {
      // `untitled` is editor-only; preview panes have no such field.
      const nextContent =
        content.kind === 'editor'
          ? {
              ...content,
              filePath: newPath,
              ...(options?.untitled === undefined ? { untitled: undefined } : { untitled: options.untitled }),
            }
          : { ...content, filePath: newPath }
      return {
        type: 'leaf',
        pane: { ...layout.pane, content: nextContent },
      }
    }
    return layout
  }

  const children = layout.children.map((child) => renameEditorPanesInLayout(child, source, oldPath, newPath, options))
  return children.some((child, index) => child !== layout.children[index])
    ? { ...layout, children }
    : layout
}

export interface OpenSingletonOpts {
  /**
   * Insert the new tab right after this tabId in tabOrder. Caller is
   * responsible for computing this — typically via
   * `computeClusterInsertTarget` so the same value can be forwarded to
   * `useWorkspaceStore.insertTab` and keep workspace.tabs / tabOrder
   * agreed on placement (the TabBar renders from workspace.tabs, so a
   * mismatch silently regresses the clustering UX).
   */
  afterTabId?: string
}

interface TabState {
  tabs: Record<string, Tab>
  tabOrder: string[]
  activeTabId: string | null
  visitHistory: string[]

  addTab: (tab: Tab, afterTabId?: string) => void
  openSingletonTab: (content: PaneContent, opts?: OpenSingletonOpts) => string
  closeTab: (id: string) => void
  setActiveTab: (id: string | null) => void
  setViewMode: (tabId: string, paneId: string, mode: 'terminal' | 'stream') => void
  setPaneContent: (tabId: string, paneId: string, content: PaneContent) => void
  renameEditorPanes: (source: FileSource, oldPath: string, newPath: string, options?: { untitled?: UntitledDocumentState }) => void
  splitPane: (tabId: string, paneId: string, direction: 'h' | 'v', content: PaneContent) => void
  splitPaneBlank: (tabId: string, paneId: string, direction: 'h' | 'v') => void
  closePane: (tabId: string, paneId: string) => void
  remountPane: (tabId: string, paneId: string) => string | null
  resizePanes: (tabId: string, splitId: string, sizes: number[]) => void
  applyLayout: (tabId: string, pattern: LayoutPattern) => void
  setTabLayout: (tabId: string, layout: PaneLayout) => void
  detachPane: (tabId: string, paneId: string, afterTabId?: string) => string | null
  reorderTabs: (order: string[]) => void
  togglePin: (id: string) => void
  toggleLock: (id: string) => void
  updateSessionCache: (hostId: string, sessionCode: string, cachedName: string, tmuxInstance: string) => void
  setPaneRebuild: (hostId: string, sessionCode: string, expectedTmuxInstance: string, patch: RebuildPatch) => void
  setPaneRebuildForPane: (
    tabId: string,
    paneId: string,
    expected: { hostId: string; sessionCode: string; tmuxInstance: string },
    patch: Extract<RebuildPatch, { kind: 'field' }>,
  ) => void
  markTerminated: (hostId: string, sessionCode: string, reason: TerminatedReason) => void
  markTerminatedForGeneration: (hostId: string, sessionCode: string, expectedTmuxInstance: string, reason: TerminatedReason) => void
  adoptTmuxInstance: (hostId: string, sessionCode: string, tmuxInstance: string) => void
  markHostTerminated: (hostId: string, reason: TerminatedReason) => void
}

export const useTabStore = create<TabState>()(
  persist(
    (set, get) => ({
      tabs: {},
      tabOrder: [],
      activeTabId: null,
      visitHistory: [],

      addTab: (tab, afterTabId) =>
        set((state) => {
          if (state.tabs[tab.id]) return state // dedup guard
          let newOrder: string[]
          if (afterTabId) {
            const idx = state.tabOrder.indexOf(afterTabId)
            if (idx !== -1) {
              // If afterTabId is pinned and new tab is not, skip past pinned group
              let insertIdx = idx + 1
              if (!tab.pinned && state.tabs[afterTabId]?.pinned) {
                while (insertIdx < state.tabOrder.length && state.tabs[state.tabOrder[insertIdx]]?.pinned) {
                  insertIdx++
                }
              }
              newOrder = [...state.tabOrder]
              newOrder.splice(insertIdx, 0, tab.id)
            } else {
              newOrder = [...state.tabOrder, tab.id]
            }
          } else {
            newOrder = [...state.tabOrder, tab.id]
          }
          return {
            tabs: { ...state.tabs, [tab.id]: tab },
            tabOrder: newOrder,
            activeTabId: state.activeTabId ?? tab.id,
          }
        }),

      openSingletonTab: (content, opts) => {
        const state = get()
        // Scan all tabs' primary pane for matching content
        for (const id of state.tabOrder) {
          const tab = state.tabs[id]
          if (!tab) continue
          const primary = getPrimaryPane(tab.layout)
          if (contentMatches(primary.content, content)) {
            get().setActiveTab(id)
            return id
          }
        }
        // Not found — create + insert at caller-supplied position
        // (or append at end). Workspace insertion (ws.tabs) is the
        // caller's responsibility.
        const tab = createTab(content)
        get().addTab(tab, opts?.afterTabId)
        get().setActiveTab(tab.id)
        return tab.id
      },

      closeTab: (id) =>
        set((state) => {
          if (!state.tabs[id]) return state
          if (state.tabs[id].locked) return state
           
          const { [id]: _removed, ...remainingTabs } = state.tabs
          const newOrder = state.tabOrder.filter((tid) => tid !== id)
          // Clean closed tab from visitHistory; active tab selection is caller's responsibility
          const newHistory = state.visitHistory.filter((tid) => tid !== id)
          return {
            tabs: remainingTabs,
            tabOrder: newOrder,
            activeTabId: state.activeTabId === id ? null : state.activeTabId,
            visitHistory: newHistory,
          }
        }),

      setActiveTab: (id) =>
        set((state) => {
          if (id === null) return { activeTabId: null }
          if (!state.tabs[id]) return state
          if (id === state.activeTabId) return state
          // Record current tab in visitHistory (dedup: remove newId from history first)
          const newHistory = state.activeTabId !== null
            ? [...state.visitHistory.filter((tid) => tid !== id), state.activeTabId]
            : state.visitHistory.filter((tid) => tid !== id)
          return { activeTabId: id, visitHistory: newHistory }
        }),

      setViewMode: (tabId, paneId, mode) =>
        set((state) => {
          const tab = state.tabs[tabId]
          if (!tab) return state
          const pane = findPane(tab.layout, paneId)
          if (!pane || pane.content.kind !== 'tmux-session') return state
          const newLayout = updatePaneInLayout(tab.layout, paneId, {
            kind: 'tmux-session',
            hostId: pane.content.hostId,
            sessionCode: pane.content.sessionCode,
            mode,
            cachedName: pane.content.cachedName,
            tmuxInstance: pane.content.tmuxInstance,
            // Carried explicitly: this content is rebuilt field by field, and
            // the rebuild record describes the tmux session, not the view.
            rebuild: pane.content.rebuild,
          })
          return { tabs: { ...state.tabs, [tabId]: { ...tab, layout: newLayout } } }
        }),

      setPaneContent: (tabId, paneId, content) =>
        set((state) => {
          const tab = state.tabs[tabId]
          if (!tab) return state
          const newLayout = updatePaneInLayout(tab.layout, paneId, content)
          return { tabs: { ...state.tabs, [tabId]: { ...tab, layout: newLayout } } }
        }),

      renameEditorPanes: (source, oldPath, newPath, options) =>
        set((state) => {
          let changed = false
          const tabs = { ...state.tabs }
          for (const [tabId, tab] of Object.entries(state.tabs)) {
            const newLayout = renameEditorPanesInLayout(tab.layout, source, oldPath, newPath, options)
            if (newLayout !== tab.layout) {
              tabs[tabId] = { ...tab, layout: newLayout }
              changed = true
            }
          }
          return changed ? { tabs } : state
        }),

      splitPane: (tabId, paneId, direction, content) =>
        set((state) => {
          const tab = state.tabs[tabId]
          if (!tab) return state
          const newLayout = splitAtPane(tab.layout, paneId, direction, content)
          if (newLayout === tab.layout) return state
          return { tabs: { ...state.tabs, [tabId]: { ...tab, layout: newLayout } } }
        }),

      splitPaneBlank: (tabId, paneId, direction) =>
        set((state) => {
          const tab = state.tabs[tabId]
          if (!tab) return state
          const newLayout = splitAtPane(tab.layout, paneId, direction, { kind: 'new-tab' })
          if (newLayout === tab.layout) return state
          return { tabs: { ...state.tabs, [tabId]: { ...tab, layout: newLayout } } }
        }),

      closePane: (tabId, paneId) => {
        const state = get()
        const tab = state.tabs[tabId]
        if (!tab) return
        const newLayout = removePane(tab.layout, paneId)
        if (newLayout === null) {
          get().closeTab(tabId)
          return
        }
        set({ tabs: { ...state.tabs, [tabId]: { ...tab, layout: newLayout } } })
      },

      remountPane: (tabId, paneId) => {
        const state = get()
        const tab = state.tabs[tabId]
        if (!tab) return null
        // Swap the leaf's pane.id in place → new React key → forced remount of
        // just that leaf (Phase 2c restore preview refresh). Returns the new id.
        const res = remountLeaf(tab.layout, paneId)
        if (!res) return null
        set({ tabs: { ...state.tabs, [tabId]: { ...tab, layout: res.layout } } })
        return res.newPaneId
      },

      resizePanes: (tabId, splitId, sizes) =>
        set((state) => {
          const tab = state.tabs[tabId]
          if (!tab) return state
          const update = (layout: PaneLayout): PaneLayout => {
            if (layout.type === 'leaf') return layout
            if (layout.id === splitId) return { ...layout, sizes }
            const newChildren = layout.children.map(update)
            return newChildren.some((c, i) => c !== layout.children[i])
              ? { ...layout, children: newChildren }
              : layout
          }
          const newLayout = update(tab.layout)
          if (newLayout === tab.layout) return state
          return { tabs: { ...state.tabs, [tabId]: { ...tab, layout: newLayout } } }
        }),

      applyLayout: (tabId, pattern) =>
        set((state) => {
          const tab = state.tabs[tabId]
          if (!tab) return state
          const newLayout = applyLayoutPattern(tab.layout, pattern)
          return { tabs: { ...state.tabs, [tabId]: { ...tab, layout: newLayout } } }
        }),

      setTabLayout: (tabId, layout) =>
        set((state) => {
          const tab = state.tabs[tabId]
          if (!tab) return state
          return { tabs: { ...state.tabs, [tabId]: { ...tab, layout } } }
        }),

      detachPane: (tabId, paneId, afterTabId) => {
        const state = get()
        const tab = state.tabs[tabId]
        if (!tab) return null
        const pane = findPane(tab.layout, paneId)
        if (!pane) return null
        if (tab.layout.type === 'leaf') return null
        const newLayout = removePane(tab.layout, paneId)
        if (!newLayout) return null
        const newTab = createTab(pane.content)
        let newOrder: string[]
        if (afterTabId) {
          const idx = state.tabOrder.indexOf(afterTabId)
          if (idx !== -1) {
            newOrder = [...state.tabOrder]
            newOrder.splice(idx + 1, 0, newTab.id)
          } else {
            newOrder = [...state.tabOrder, newTab.id]
          }
        } else {
          newOrder = [...state.tabOrder, newTab.id]
        }
        set({
          tabs: { ...state.tabs, [tabId]: { ...tab, layout: newLayout }, [newTab.id]: newTab },
          tabOrder: newOrder,
        })
        return newTab.id
      },

      reorderTabs: (order) =>
        set({ tabOrder: order }),

      togglePin: (id) =>
        set((state) => {
          const tab = state.tabs[id]
          if (!tab) return state
          const newPinned = !tab.pinned
          const updated = { ...tab, pinned: newPinned }
          const newOrder = state.tabOrder.filter((tid) => tid !== id)
          const firstNormalIdx = newOrder.findIndex((tid) => !state.tabs[tid]?.pinned)
          const insertIdx = firstNormalIdx === -1 ? newOrder.length : firstNormalIdx
          newOrder.splice(insertIdx, 0, id)
          return { tabs: { ...state.tabs, [id]: updated }, tabOrder: newOrder }
        }),

      toggleLock: (id) =>
        set((state) => {
          const tab = state.tabs[id]
          if (!tab) return state
          return { tabs: { ...state.tabs, [id]: { ...tab, locked: !tab.locked } } }
        }),

      // Generation-scoped, and every leaf — not just the primary pane, which
      // left a split tab's second terminal stuck on the old name. Without the
      // generation match a rename broadcast from a new tmux server would write
      // the new name onto the old pane the reconciler is about to mark dead,
      // and pollute the `rebuild.sessionName` the rebuild would then use.
      updateSessionCache: (hostId, sessionCode, cachedName, tmuxInstance) =>
        set((state) => {
          let changed = false
          const tabs = { ...state.tabs }
          for (const [id, tab] of Object.entries(tabs)) {
            const newLayout = mapTmuxPanesInLayout(
              tab.layout,
              (c): c is TmuxSessionContent => sessionBindingMatches(c, hostId, sessionCode, tmuxInstance),
              (c) => applySessionName(c, cachedName),
            )
            if (newLayout !== tab.layout) {
              tabs[id] = { ...tab, layout: newLayout }
              changed = true
            }
          }
          return changed ? { tabs } : state
        }),

      // Session-scoped: the agent group and the cwd probe describe the tmux
      // SESSION, so they land on every LIVE pane bound to the triple (spec
      // §4.1) — see `acceptsSessionScopedWrite` for why terminated is excluded
      // here rather than only at the reader.
      setPaneRebuild: (hostId, sessionCode, expectedTmuxInstance, patch) =>
        set((state) => {
          let changed = false
          const tabs = { ...state.tabs }
          for (const [id, tab] of Object.entries(tabs)) {
            const newLayout = mapTmuxPanesInLayout(
              tab.layout,
              (c): c is TmuxSessionContent => acceptsSessionScopedWrite(c, hostId, sessionCode, expectedTmuxInstance),
              (c) => applyRebuildPatch(c, patch),
            )
            if (newLayout !== tab.layout) {
              tabs[id] = { ...tab, layout: newLayout }
              changed = true
            }
          }
          return changed ? { tabs } : state
        }),

      // Pane-scoped: a user edit belongs to the pane it was made on, so
      // editing one pane's cwd leaves its split sibling's record alone
      // (spec §4.10 gives each pane its own block, §4.11 resolves conflicting
      // per-pane edits — both need the edits to be able to differ).
      setPaneRebuildForPane: (tabId, paneId, expected, patch) =>
        set((state) => {
          const tab = state.tabs[tabId]
          if (!tab) return state
          const pane = findPane(tab.layout, paneId)
          if (!pane) return state
          const c = pane.content
          if (!rebuildBindingMatches(c, expected.hostId, expected.sessionCode, expected.tmuxInstance)) return state
          const next = applyRebuildPatch(c, patch)
          if (next === c) return state
          return { tabs: { ...state.tabs, [tabId]: { ...tab, layout: updatePaneInLayout(tab.layout, paneId, next) } } }
        }),

      markTerminated: (hostId, sessionCode, reason) =>
        set((state) => {
          let changed = false
          const tabs = { ...state.tabs }
          for (const [id, tab] of Object.entries(tabs)) {
            const newLayout = markPanesInLayout(tab.layout, hostId, sessionCode, reason)
            if (newLayout !== tab.layout) {
              tabs[id] = { ...tab, layout: newLayout }
              changed = true
            }
          }
          return changed ? { tabs } : state
        }),

      // Generation-scoped sibling of markTerminated (spec §4.5). markTerminated
      // stays for its existing session-closed / host-removed callers.
      markTerminatedForGeneration: (hostId, sessionCode, expectedTmuxInstance, reason) =>
        set((state) => {
          let changed = false
          const tabs = { ...state.tabs }
          for (const [id, tab] of Object.entries(tabs)) {
            const newLayout = markPanesInLayout(tab.layout, hostId, sessionCode, reason, expectedTmuxInstance)
            if (newLayout !== tab.layout) {
              tabs[id] = { ...tab, layout: newLayout }
              changed = true
            }
          }
          return changed ? { tabs } : state
        }),

      adoptTmuxInstance: (hostId, sessionCode, tmuxInstance) =>
        set((state) => {
          if (!tmuxInstance) return state // "" means unknown — never stamp it
          let changed = false
          const tabs = { ...state.tabs }
          for (const [id, tab] of Object.entries(tabs)) {
            const newLayout = adoptInstanceInLayout(tab.layout, hostId, sessionCode, tmuxInstance)
            if (newLayout !== tab.layout) {
              tabs[id] = { ...tab, layout: newLayout }
              changed = true
            }
          }
          return changed ? { tabs } : state
        }),

      markHostTerminated: (hostId, reason) =>
        set((state) => {
          let changed = false
          const tabs = { ...state.tabs }
          for (const [id, tab] of Object.entries(tabs)) {
            const newLayout = markHostPanesInLayout(tab.layout, hostId, reason)
            if (newLayout !== tab.layout) {
              tabs[id] = { ...tab, layout: newLayout }
              changed = true
            }
          }
          return changed ? { tabs } : state
        }),
    }),
    {
      name: STORAGE_KEYS.TABS,
      storage: purdexStorage,
      version: 2,
      migrate: migrateTabStore,
      partialize: (state) => ({
        tabs: state.tabs,
        tabOrder: state.tabOrder,
        activeTabId: state.activeTabId,
      }),
    },
  ),
)

syncManager.register(STORAGE_KEYS.TABS, useTabStore)
