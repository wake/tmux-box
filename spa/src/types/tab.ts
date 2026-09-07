import { generateId } from '../lib/id'
import type { FileSource } from './fs'

// === Tab (tab bar unit) ===
export interface Tab {
  id: string
  pinned: boolean
  locked: boolean
  createdAt: number
  layout: PaneLayout
}

// === Pane Layout (tab-internal split tree) ===
export type SplitLayout = { type: 'split'; id: string; direction: 'h' | 'v'; children: PaneLayout[]; sizes: number[] }
export type PaneLayout =
  | { type: 'leaf'; pane: Pane }
  | SplitLayout

export type LayoutPattern = 'single' | 'split-h' | 'split-v'

// === Pane (content slot) ===
export interface Pane {
  id: string
  content: PaneContent
}

// === Pane Content (discriminated union) ===
export type TerminatedReason = 'session-closed' | 'tmux-restarted' | 'host-removed'

export interface UntitledDocumentState {
  name: string
  suggestedExtension: '.txt' | '.md'
  hasBeenRenamed: boolean
}

/**
 * Everything needed to recreate the tmux session a pane was bound to, once
 * that session is gone (spec §4.1). Accumulated in place by three ranked
 * writers — see `setPaneRebuild` in `useTabStore`.
 *
 * Optional on the pane: alpha convention is no persist migration, so panes
 * persisted before this feature simply have no record and behave as before.
 */
export interface PaneRebuildRecord {
  /** tmux session name actually in use (including any collision suffix). */
  sessionName: string
  /** Generation this record describes: the host's tmux_instance at write time. */
  tmuxInstance: string
  /** cwd the agent was launched in — the cwd its resume is scoped to. */
  cwd?: string
  cwdSource?: 'agent-session-start' | 'pane-probe'
  agent?: {
    /** 'cc' | 'codex' | 'opencode' — open string, mirrors AGENT_NAMES. */
    type: string
    sessionId?: string
    /** tmux pane the owning SessionStart came from (spec §4.4). */
    tmuxPaneId?: string
    updatedAt: number
  }
  resumeCommand?: string
  /**
   * The record's agent disagrees with what the daemon currently reports for
   * the session (spec §9.1): still shown, but unchecked by default and skipped
   * by "Rebuild all".
   */
  unverified?: boolean
  capturedAt: number
}

export type PaneContent =
  | { kind: 'new-tab' }
  | { kind: 'tmux-session'; hostId: string; sessionCode: string; mode: 'terminal' | 'stream'; cachedName: string; tmuxInstance: string; terminated?: TerminatedReason; rebuild?: PaneRebuildRecord }
  | { kind: 'dashboard' }
  | { kind: 'hosts' }
  | { kind: 'history' }
  | { kind: 'settings'; scope: 'global' | { workspaceId: string } }
  | { kind: 'browser'; url: string }
  | { kind: 'memory-monitor' }
  | { kind: 'editor'; source: FileSource; filePath: string; untitled?: UntitledDocumentState; diff?: { against: 'saved' | string } }
  | { kind: 'editor-buffers' }
  | { kind: 'image-preview'; source: FileSource; filePath: string }
  | { kind: 'pdf-preview'; source: FileSource; filePath: string }
  // Execution detail page (M0 dispatch, Task P.12). Observe-only landing when a
  // deeplink cannot focus a live session tab; `host` is the optional daemon hint
  // carried by the deeplink. Never attaches a stdin write path.
  | { kind: 'execution'; executionId: string; host?: string }

/** The `tmux-session` arm of `PaneContent`, named so writers can talk about it. */
export type TmuxSessionContent = Extract<PaneContent, { kind: 'tmux-session' }>

/**
 * One write against a pane's `PaneRebuildRecord`. The kinds are the writer
 * ranking of spec §4.1, and that ranking is the whole concurrency policy:
 *
 * - `agent-group` — a qualifying SessionStart. Replaces `agent`, `cwd`,
 *   `cwdSource`, `resumeCommand` and `capturedAt` **as one unit**; a payload
 *   without `cwd` clears `cwd` rather than leaving the previous agent's
 *   directory attached to a new session id.
 * - `field` — a user edit. Touches only the field named.
 * - `probe-cwd` — the pane cwd probe. Fills `cwd` only when it is unset;
 *   never promoted to agent provenance.
 * - `unverified` — the reconnect projection disagrees with `agent.type`.
 */
export type RebuildPatch =
  | { kind: 'agent-group'; record: Omit<PaneRebuildRecord, 'sessionName'> }
  | { kind: 'field'; field: 'cwd' | 'resumeCommand' | 'sessionName'; value: string }
  | { kind: 'probe-cwd'; cwd: string }
  | { kind: 'unverified'; unverified: boolean }

// === Workspace ===
export type IconWeight = 'bold' | 'regular' | 'thin' | 'light' | 'fill' | 'duotone'

export interface Workspace {
  id: string
  name: string
  icon?: string
  iconWeight?: IconWeight
  tabs: string[]
  activeTabId: string | null
  moduleConfig?: Record<string, Record<string, unknown>>
}
// === Factories ===

export function createTab(content: PaneContent, opts?: { pinned?: boolean }): Tab {
  return {
    id: generateId(),
    pinned: opts?.pinned ?? false,
    locked: false,
    createdAt: Date.now(),
    layout: { type: 'leaf', pane: { id: generateId(), content } },
  }
}

export function createWorkspace(name: string, icon?: string): Workspace {
  return {
    id: generateId(),
    name,
    icon,
    tabs: [],
    activeTabId: null,
    moduleConfig: {},
  }
}

export function isStandaloneTab(tabId: string, workspaces: Workspace[]): boolean {
  return !workspaces.some((ws) => ws.tabs.includes(tabId))
}
