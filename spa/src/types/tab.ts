export interface Tab {
  id: string
  type: string
  label: string
  icon: string
  hostId: string
  viewMode?: string
  data: Record<string, unknown>
  pinned: boolean
  locked: boolean
}

export interface Workspace {
  id: string
  name: string
  color: string
  icon?: string
  directories: PinnedItem[]
  tabs: string[]
  activeTabId: string | null
  sidebarState: WorkspaceSidebarState
}

export interface PinnedItem {
  type: 'directory' | 'file'
  hostId: string
  path: string
}

export type SidebarZone = 'left-outer' | 'left-inner' | 'right-inner' | 'right-outer'

export interface WorkspaceSidebarState {
  zones: Record<SidebarZone, {
    activePanelId?: string
    width: number
    mode: 'fixed' | 'default' | 'collapsed'
  }>
}

const WORKSPACE_COLORS = ['#7a6aaa', '#6aaa7a', '#aa6a7a', '#6a8aaa', '#aa8a6a', '#8a6aaa']

function generateId(): string {
  if (typeof crypto !== 'undefined' && crypto.randomUUID) return crypto.randomUUID()
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
}

function defaultSidebarState(): WorkspaceSidebarState {
  const defaultZone = { width: 200, mode: 'default' as const }
  return {
    zones: {
      'left-outer': { ...defaultZone },
      'left-inner': { ...defaultZone },
      'right-inner': { ...defaultZone },
      'right-outer': { ...defaultZone },
    },
  }
}

export interface CreateSessionTabOpts {
  label: string
  hostId: string
  sessionName: string
  viewMode?: 'terminal' | 'stream'
  icon?: string
}

export interface CreateEditorTabOpts {
  label: string
  hostId: string
  filePath: string
  isDirty?: boolean
  icon?: string
}

export function createSessionTab(opts: CreateSessionTabOpts): Tab {
  return {
    id: generateId(),
    type: 'session',
    label: opts.label,
    icon: opts.icon ?? 'Terminal',
    hostId: opts.hostId,
    viewMode: opts.viewMode ?? 'terminal',
    data: { sessionName: opts.sessionName },
    pinned: false,
    locked: false,
  }
}

export function createEditorTab(opts: CreateEditorTabOpts): Tab {
  return {
    id: generateId(),
    type: 'editor',
    label: opts.label,
    icon: opts.icon ?? 'File',
    hostId: opts.hostId,
    data: { filePath: opts.filePath, isDirty: opts.isDirty ?? false },
    pinned: false,
    locked: false,
  }
}

export function createTab(opts: { type: string; label: string; hostId: string; icon?: string; viewMode?: string; data?: Record<string, unknown> }): Tab {
  return {
    id: generateId(),
    type: opts.type,
    label: opts.label,
    icon: opts.icon ?? '',
    hostId: opts.hostId,
    viewMode: opts.viewMode,
    data: opts.data ?? {},
    pinned: false,
    locked: false,
  }
}

export function createWorkspace(opts: { name: string; color?: string; icon?: string }): Workspace {
  return {
    id: generateId(),
    name: opts.name,
    color: opts.color ?? WORKSPACE_COLORS[Math.floor(Math.random() * WORKSPACE_COLORS.length)],
    icon: opts.icon,
    directories: [],
    tabs: [],
    activeTabId: null,
    sidebarState: defaultSidebarState(),
  }
}

export function isStandaloneTab(tabId: string, workspaces: Workspace[]): boolean {
  return !workspaces.some(ws => ws.tabs.includes(tabId))
}
