import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createSession, listSessions } from '../host-api'
import type { Session } from '../host-api'
import type { PaneContent, PaneLayout, Tab, Workspace } from '../../types/tab'
import type { Remap, SessionMeta, WorkspaceSnapshot } from './types'
import { remapLayoutSessions, undoLastRestore } from './restore'
import { writePrevSnapshot } from './storage'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useSessionStore } from '../../stores/useSessionStore'

vi.mock('../host-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../host-api')>()),
  listSessions: vi.fn(),
  createSession: vi.fn(),
}))

function session(overrides: Partial<Session> & { code: string }): Session {
  return {
    name: overrides.name ?? overrides.code,
    cwd: overrides.cwd ?? '/tmp',
    mode: overrides.mode ?? 'terminal',
    cc_session_id: '',
    cc_model: '',
    has_relay: false,
    ...overrides,
  }
}

function tmuxPane(
  id: string,
  hostId: string,
  sessionCode: string,
  overrides?: Partial<Extract<PaneContent, { kind: 'tmux-session' }>>,
): { type: 'leaf'; pane: { id: string; content: PaneContent } } {
  return {
    type: 'leaf',
    pane: {
      id,
      content: {
        kind: 'tmux-session',
        hostId,
        sessionCode,
        mode: 'terminal',
        cachedName: overrides?.cachedName ?? sessionCode,
        tmuxInstance: overrides?.tmuxInstance ?? 'default',
        ...overrides,
      },
    },
  }
}

function tmux(content: PaneContent): Extract<PaneContent, { kind: 'tmux-session' }> {
  if (content.kind !== 'tmux-session') throw new Error('expected tmux-session pane')
  return content
}

function paneOf(layout: PaneLayout): Extract<PaneContent, { kind: 'tmux-session' }> {
  return tmux((layout as { pane: { content: PaneContent } }).pane.content)
}

/**
 * §4.6.1: re-pointing a pane onto a session that belongs to a NEW tmux server
 * must stamp that server's generation, otherwise the generation guard (Task 6)
 * marks the freshly restored pane `'tmux-restarted'` on the very next
 * reconciliation.
 */
describe('remapLayoutSessions generation stamping', () => {
  it("1. rebuilt entry stamps the new session's tmux_instance onto the pane", () => {
    const layout = tmuxPane('p1', 'h1', 'old111', {
      tmuxInstance: '111:1000',
      terminated: 'tmux-restarted',
    })
    const remap: Remap = {
      h1: {
        old111: {
          status: 'rebuilt',
          newCode: 'new222',
          session: session({ code: 'new222', name: 'dev', tmux_instance: '222:2000' }),
        },
      },
    }

    const out = remapLayoutSessions(layout, remap, { onlyTerminated: true })

    const content = paneOf(out)
    expect(content.tmuxInstance).toBe('222:2000')
    expect(content.sessionCode).toBe('new222')
    expect(content.terminated).toBeUndefined()
  })

  it('2. reattached entry stamps the live generation too', () => {
    const layout = tmuxPane('p1', 'h1', 'live1', { tmuxInstance: '111:1000' })
    const remap: Remap = {
      h1: {
        live1: {
          status: 'reattached',
          newCode: 'live1',
          session: session({ code: 'live1', name: 'live1', tmux_instance: '333:3000' }),
        },
      },
    }

    expect(paneOf(remapLayoutSessions(layout, remap)).tmuxInstance).toBe('333:3000')
  })

  it("3. session carrying no tmux_instance falls back to the pane's existing generation", () => {
    const layout = tmuxPane('p1', 'h1', 'old1', { tmuxInstance: '111:1000' })
    const remap: Remap = {
      h1: {
        old1: {
          // Older daemon: no tmux_instance on the payload. Never downgrade a
          // known generation to ''.
          status: 'rebuilt',
          newCode: 'new1',
          session: session({ code: 'new1', name: 'new1' }),
        },
      },
    }

    expect(paneOf(remapLayoutSessions(layout, remap)).tmuxInstance).toBe('111:1000')
  })

  it('4. failed entry leaves the recorded generation untouched', () => {
    const layout = tmuxPane('p1', 'h1', 'old1', { tmuxInstance: '111:1000' })
    const remap: Remap = { h1: { old1: { status: 'failed' } } }

    const content = paneOf(remapLayoutSessions(layout, remap))
    expect(content.tmuxInstance).toBe('111:1000')
    expect(content.terminated).toBe('tmux-restarted')
  })
})

describe('undoLastRestore generation stamping', () => {
  const resetStores = (): void => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
    useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
  }

  beforeEach(() => {
    localStorage.clear()
    vi.mocked(listSessions).mockReset()
    vi.mocked(createSession).mockReset()
    resetStores()
  })

  afterEach(() => {
    vi.restoreAllMocks()
    resetStores()
    localStorage.clear()
  })

  it('5. undo re-points through restoreAll and stamps the rebuilt generation', async () => {
    const tabWithDeadPane: Tab = {
      id: 'tp',
      pinned: false,
      locked: false,
      createdAt: 0,
      layout: tmuxPane('pane-tp', 'hostA', 'dead1', {
        tmuxInstance: '111:1000',
        terminated: 'tmux-restarted',
      }),
    }
    const workspace: Workspace = {
      id: 'wsPrev',
      name: 'wsPrev',
      tabs: ['tp'],
      activeTabId: 'tp',
    }
    const sessionMeta: Record<string, Record<string, SessionMeta>> = {
      hostA: {
        dead1: {
          hostId: 'hostA',
          sessionCode: 'dead1',
          name: 'd1',
          mode: 'terminal',
          cwd: '/work',
          restorable: true,
        },
      },
    }
    const prev: WorkspaceSnapshot = {
      version: 1,
      capturedAt: 0,
      tabs: { tp: tabWithDeadPane },
      tabOrder: ['tp'],
      activeTabId: 'tp',
      workspaces: [workspace],
      activeWorkspaceId: 'wsPrev',
      sessionMeta,
    }
    writePrevSnapshot(prev)

    vi.mocked(listSessions).mockResolvedValue([])
    vi.mocked(createSession).mockResolvedValue(
      session({ code: 'new-d1', name: 'd1', tmux_instance: '444:4000' }),
    )

    const report = await undoLastRestore({ now: 7 })

    expect(report).not.toBeNull()
    const restored = paneOf(useTabStore.getState().tabs.tp.layout)
    expect(restored.sessionCode).toBe('new-d1')
    expect(restored.tmuxInstance).toBe('444:4000')
    expect(restored.terminated).toBeUndefined()
  })
})
