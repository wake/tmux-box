export interface Tab {
  id: string
  type: 'terminal' | 'stream' | 'editor'
  label: string
  icon: string
  hostId: string
  sessionName?: string
  filePath?: string
  isDirty?: boolean
}

export interface Workspace {
  id: string
  name: string
  color: string
  icon?: string
  directories: PinnedItem[]
  tabs: string[]        // tab IDs (ordered)
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
  // Fallback for insecure contexts (HTTP non-localhost)
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
}

function iconForType(type: Tab['type']): string {
  switch (type) {
    case 'terminal': return 'Terminal'
    case 'stream': return 'ChatCircleDots'
    case 'editor': return 'File'
  }
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

export function createTab(opts: Omit<Tab, 'id' | 'icon'> & { icon?: string }): Tab {
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  const { isDirty, ...rest } = opts
  return {
    ...rest,
    id: generateId(),
    icon: opts.icon ?? iconForType(opts.type),
    isDirty: opts.type === 'editor' ? (opts.isDirty ?? false) : undefined,
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
