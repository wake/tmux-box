import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { SnapshotSettingsSection } from './SnapshotSettingsSection'
import { RestoreError } from '../../lib/snapshot/types'
import type { RestoreReport, SessionMeta, WorkspaceSnapshot } from '../../lib/snapshot/types'
import type { Session } from '../../lib/host-api'
import type { PaneContent, Tab, Workspace } from '../../types/tab'
import * as storageModule from '../../lib/snapshot/storage'
import * as hostApiModule from '../../lib/host-api'
import * as captureModule from '../../lib/snapshot/capture'
import * as restoreModule from '../../lib/snapshot/restore'
import { useRebuildStore } from '../../stores/useRebuildStore'

// Partial mock: stub the storage read/write boundary but keep the REAL
// setSessionMetaCwd so the cwd-edit → restorable-flip drives end-to-end.
vi.mock('../../lib/snapshot/storage', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../lib/snapshot/storage')>()
  return {
    ...actual,
    readSnapshot: vi.fn(),
    writeSnapshot: vi.fn(),
    readPrevSnapshot: vi.fn(),
    writePrevSnapshot: vi.fn(),
  }
})
vi.mock('../../lib/host-api')
vi.mock('../../lib/snapshot/capture')
vi.mock('../../lib/snapshot/restore')

const EMPTY_REPORT: RestoreReport = { reattached: 0, rebuilt: 0, failed: 0, rebuiltButUnattached: [] }

// ---- fixtures ------------------------------------------------------------

function meta(over: Partial<SessionMeta> & Pick<SessionMeta, 'hostId' | 'sessionCode' | 'name'>): SessionMeta {
  return {
    mode: 'terminal',
    restorable: true,
    ...over,
  }
}

function session(over: Partial<Session> & Pick<Session, 'code' | 'name'>): Session {
  return {
    cwd: '/tmp',
    mode: 'terminal',
    cc_session_id: '',
    cc_model: '',
    has_relay: false,
    ...over,
  }
}

function leafTab(id: string, content: PaneContent): Tab {
  return {
    id,
    pinned: false,
    locked: false,
    createdAt: 0,
    layout: { type: 'leaf', pane: { id: `${id}-pane`, content } },
  }
}

function ws(over: Partial<Workspace> & Pick<Workspace, 'id' | 'name' | 'tabs'>): Workspace {
  return { activeTabId: null, ...over }
}

function makeSnapshot(over: Partial<WorkspaceSnapshot>): WorkspaceSnapshot {
  return {
    version: 1,
    capturedAt: Date.now(),
    tabs: {},
    tabOrder: [],
    activeTabId: null,
    workspaces: [],
    activeWorkspaceId: null,
    sessionMeta: {},
    ...over,
  }
}

const mockedReadSnapshot = vi.mocked(storageModule.readSnapshot)
const mockedWriteSnapshot = vi.mocked(storageModule.writeSnapshot)
const mockedReadPrev = vi.mocked(storageModule.readPrevSnapshot)
const mockedListSessions = vi.mocked(hostApiModule.listSessions)
const mockedCapture = vi.mocked(captureModule.captureSnapshot)
const mockedRebuildAll = vi.mocked(restoreModule.rebuildAllSessions)
const mockedRestoreTab = vi.mocked(restoreModule.restoreTabLayout)
const mockedRestoreAll = vi.mocked(restoreModule.restoreAll)
const mockedUndo = vi.mocked(restoreModule.undoLastRestore)

beforeEach(() => {
  vi.clearAllMocks()
  useRebuildStore.setState({ operations: {}, lockedBy: null })
  mockedReadPrev.mockReturnValue(null)
  mockedListSessions.mockResolvedValue([])
  // Safe defaults so an unstubbed click never crashes on `await undefined`.
  mockedCapture.mockResolvedValue({ total: 0, resolved: 0, unresolved: 0 })
  mockedRebuildAll.mockResolvedValue(EMPTY_REPORT)
  mockedRestoreTab.mockResolvedValue(EMPTY_REPORT)
  mockedRestoreAll.mockResolvedValue(EMPTY_REPORT)
  mockedUndo.mockResolvedValue(EMPTY_REPORT)
})

/** A snapshot with one workspace/tab/session so all blocks + buttons render. */
function snapWithData(): WorkspaceSnapshot {
  const tab = leafTab('t1', {
    kind: 'tmux-session',
    hostId: 'h1',
    sessionCode: 's1',
    mode: 'terminal',
    cachedName: 'work',
    tmuxInstance: 'default',
  })
  return makeSnapshot({
    tabs: { t1: tab },
    tabOrder: ['t1'],
    workspaces: [ws({ id: 'w1', name: 'Alpha', tabs: ['t1'] })],
    activeWorkspaceId: 'w1',
    sessionMeta: { h1: { s1: meta({ hostId: 'h1', sessionCode: 's1', name: 'work', cwd: '/x' }) } },
  })
}

describe('SnapshotSettingsSection — health reconciliation (T8)', () => {
  it('live list missing the captured code → row is dead-rebuildable (red)', async () => {
    mockedReadSnapshot.mockReturnValue(
      makeSnapshot({
        sessionMeta: { h1: { s1: meta({ hostId: 'h1', sessionCode: 's1', name: 'work', cwd: '/x', restorable: true }) } },
      }),
    )
    // Host reachable but returns a DIFFERENT code — no match.
    mockedListSessions.mockResolvedValue([session({ code: 'other', name: 'other' })])

    render(<SnapshotSettingsSection />)
    await waitFor(() => {
      expect(screen.getByTestId('snapshot-health-h1-s1').getAttribute('data-health')).toBe('dead')
    })
  })

  it('live list with matching code AND name → row is live (green)', async () => {
    mockedReadSnapshot.mockReturnValue(
      makeSnapshot({
        sessionMeta: { h1: { s1: meta({ hostId: 'h1', sessionCode: 's1', name: 'work', cwd: '/x' }) } },
      }),
    )
    mockedListSessions.mockResolvedValue([session({ code: 's1', name: 'work' })])

    render(<SnapshotSettingsSection />)
    await waitFor(() => {
      expect(screen.getByTestId('snapshot-health-h1-s1').getAttribute('data-health')).toBe('live')
    })
  })

  it('code matches but name differs → NOT live, falls through to dead (red)', async () => {
    mockedReadSnapshot.mockReturnValue(
      makeSnapshot({
        sessionMeta: { h1: { s1: meta({ hostId: 'h1', sessionCode: 's1', name: 'work', cwd: '/x', restorable: true }) } },
      }),
    )
    // Same code, DIFFERENT name → a reused code after a tmux restart.
    mockedListSessions.mockResolvedValue([session({ code: 's1', name: 'somethingElse' })])

    render(<SnapshotSettingsSection />)
    await waitFor(() => {
      expect(screen.getByTestId('snapshot-health-h1-s1').getAttribute('data-health')).toBe('dead')
    })
  })

  it('restorable:true but cwd missing → structure-only (yellow), NOT dead — engine cannot rebuild without cwd (F2)', async () => {
    mockedReadSnapshot.mockReturnValue(
      makeSnapshot({
        sessionMeta: {
          h1: { s1: meta({ hostId: 'h1', sessionCode: 's1', name: 'work', restorable: true, cwd: undefined }) },
        },
      }),
    )
    // Host reachable, no matching session → would be 🔴 dead if cwd were present.
    mockedListSessions.mockResolvedValue([session({ code: 'other', name: 'other' })])

    render(<SnapshotSettingsSection />)
    await waitFor(() => {
      expect(screen.getByTestId('snapshot-health-h1-s1').getAttribute('data-health')).toBe('structure')
    })
  })

  it('restorable:false → structure-only (yellow)', async () => {
    mockedReadSnapshot.mockReturnValue(
      makeSnapshot({
        sessionMeta: {
          h1: { s1: meta({ hostId: 'h1', sessionCode: 's1', name: 'work', restorable: false }) },
        },
      }),
    )
    mockedListSessions.mockResolvedValue([])

    render(<SnapshotSettingsSection />)
    await waitFor(() => {
      expect(screen.getByTestId('snapshot-health-h1-s1').getAttribute('data-health')).toBe('structure')
    })
  })

  it('listSessions rejects → every row for that host is host-offline (gray)', async () => {
    mockedReadSnapshot.mockReturnValue(
      makeSnapshot({
        sessionMeta: {
          h1: {
            s1: meta({ hostId: 'h1', sessionCode: 's1', name: 'a', cwd: '/x' }),
            s2: meta({ hostId: 'h1', sessionCode: 's2', name: 'b', restorable: false }),
          },
        },
      }),
    )
    mockedListSessions.mockRejectedValue(new Error('offline'))

    render(<SnapshotSettingsSection />)
    await waitFor(() => {
      expect(screen.getByTestId('snapshot-health-h1-s1').getAttribute('data-health')).toBe('offline')
    })
    // Even the restorable:false row is offline when the host is unreachable.
    expect(screen.getByTestId('snapshot-health-h1-s2').getAttribute('data-health')).toBe('offline')
  })

  it('calls listSessions exactly once per captured host', async () => {
    mockedReadSnapshot.mockReturnValue(
      makeSnapshot({
        sessionMeta: {
          h1: { s1: meta({ hostId: 'h1', sessionCode: 's1', name: 'a', cwd: '/x' }) },
          h2: { s2: meta({ hostId: 'h2', sessionCode: 's2', name: 'b', cwd: '/y' }) },
        },
      }),
    )
    render(<SnapshotSettingsSection />)
    await waitFor(() => {
      expect(mockedListSessions).toHaveBeenCalledTimes(2)
    })
    expect(mockedListSessions).toHaveBeenCalledWith('h1')
    expect(mockedListSessions).toHaveBeenCalledWith('h2')
  })
})

describe('SnapshotSettingsSection — tabs tree (T8 block 2)', () => {
  it('renders workspace → tab → pane labels (terminal name / editor path / browser url)', () => {
    const tabs: Record<string, Tab> = {
      t1: leafTab('t1', { kind: 'tmux-session', hostId: 'h1', sessionCode: 's1', mode: 'terminal', cachedName: 'my-term', tmuxInstance: 'default' }),
      t2: leafTab('t2', { kind: 'editor', source: { kind: 'local' } as never, filePath: '/repo/main.ts' }),
      t3: leafTab('t3', { kind: 'browser', url: 'https://example.test/page' }),
    }
    mockedReadSnapshot.mockReturnValue(
      makeSnapshot({
        tabs,
        tabOrder: ['t1', 't2', 't3'],
        workspaces: [ws({ id: 'w1', name: 'Alpha', tabs: ['t1', 't2', 't3'] })],
        activeWorkspaceId: 'w1',
      }),
    )

    render(<SnapshotSettingsSection />)
    expect(screen.getByText('Alpha')).toBeTruthy()
    expect(screen.getByText('my-term')).toBeTruthy()
    expect(screen.getByText('/repo/main.ts')).toBeTruthy()
    expect(screen.getByText('https://example.test/page')).toBeTruthy()
  })
})

describe('SnapshotSettingsSection — empty state (T8)', () => {
  it('no snapshot → shows only capture button + empty message, no tables', () => {
    mockedReadSnapshot.mockReturnValue(null)

    render(<SnapshotSettingsSection />)
    expect(screen.getByTestId('snapshot-capture-btn')).toBeTruthy()
    expect(screen.getByTestId('snapshot-empty')).toBeTruthy()
    expect(screen.queryByTestId('snapshot-tmux-block')).toBeNull()
    expect(screen.queryByTestId('snapshot-tabs-block')).toBeNull()
    expect(mockedListSessions).not.toHaveBeenCalled()
  })
})

describe('SnapshotSettingsSection — action wiring (T9)', () => {
  it('capture button → captureSnapshot(Date.now()) + success toast summarizing result', async () => {
    mockedReadSnapshot.mockReturnValue(null)
    mockedCapture.mockResolvedValue({ total: 4, resolved: 3, unresolved: 1 })

    render(<SnapshotSettingsSection />)
    fireEvent.click(screen.getByTestId('snapshot-capture-btn'))

    await waitFor(() => {
      expect(mockedCapture).toHaveBeenCalledTimes(1)
    })
    // Only the UI layer supplies the clock.
    expect(mockedCapture).toHaveBeenCalledWith(expect.any(Number))
    const status = await screen.findByTestId('snapshot-status')
    expect(status.getAttribute('data-tone')).toBe('success')
    expect(status.getAttribute('data-total')).toBe('4')
    expect(status.getAttribute('data-unresolved')).toBe('1')
  })

  it('rebuild button → rebuildAllSessions(snap)', async () => {
    const snap = snapWithData()
    mockedReadSnapshot.mockReturnValue(snap)

    render(<SnapshotSettingsSection />)
    fireEvent.click(screen.getByTestId('snapshot-rebuild-btn'))

    await waitFor(() => {
      expect(mockedRebuildAll).toHaveBeenCalledTimes(1)
    })
    expect(mockedRebuildAll).toHaveBeenCalledWith(snap)
  })

  it('restore-tab button → restoreTabLayout(snap)', async () => {
    const snap = snapWithData()
    mockedReadSnapshot.mockReturnValue(snap)

    render(<SnapshotSettingsSection />)
    fireEvent.click(screen.getByTestId('snapshot-restore-tab-btn'))

    await waitFor(() => {
      expect(mockedRestoreTab).toHaveBeenCalledWith(snap)
    })
  })

  it('restore-all button → restoreAll(snap) + report toast (X reattached / Y rebuilt / Z failed)', async () => {
    const snap = snapWithData()
    mockedReadSnapshot.mockReturnValue(snap)
    mockedRestoreAll.mockResolvedValue({ reattached: 2, rebuilt: 1, failed: 3, rebuiltButUnattached: [] })

    render(<SnapshotSettingsSection />)
    fireEvent.click(screen.getByTestId('snapshot-restore-all-btn'))

    await waitFor(() => {
      expect(mockedRestoreAll).toHaveBeenCalledWith(snap)
    })
    const status = await screen.findByTestId('snapshot-status')
    expect(status.getAttribute('data-tone')).toBe('success')
    expect(status.getAttribute('data-reattached')).toBe('2')
    expect(status.getAttribute('data-rebuilt')).toBe('1')
    expect(status.getAttribute('data-failed')).toBe('3')
  })

  it('undo button → undoLastRestore(); disabled when no -prev, enabled with -prev', async () => {
    const snap = snapWithData()
    mockedReadSnapshot.mockReturnValue(snap)

    // First render: no -prev → undo disabled.
    mockedReadPrev.mockReturnValue(null)
    const { unmount } = render(<SnapshotSettingsSection />)
    expect((screen.getByTestId('snapshot-undo-btn') as HTMLButtonElement).disabled).toBe(true)
    unmount()

    // -prev present → undo enabled and wired.
    mockedReadPrev.mockReturnValue(snap)
    render(<SnapshotSettingsSection />)
    const undoBtn = screen.getByTestId('snapshot-undo-btn') as HTMLButtonElement
    expect(undoBtn.disabled).toBe(false)
    fireEvent.click(undoBtn)
    await waitFor(() => {
      expect(mockedUndo).toHaveBeenCalledTimes(1)
    })
  })

  it('RestoreError with non-empty rebuiltButUnattached → ERROR toast (restore failed) still listing name/cwd/host + console.warn (F3)', async () => {
    const snap = snapWithData()
    mockedReadSnapshot.mockReturnValue(snap)
    const report: RestoreReport = {
      reattached: 0,
      rebuilt: 1,
      failed: 0,
      rebuiltButUnattached: [{ hostId: 'h9', name: 'orphan-sess', cwd: '/srv/app' }],
    }
    mockedRestoreAll.mockRejectedValue(new RestoreError(report))
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})

    render(<SnapshotSettingsSection />)
    fireEvent.click(screen.getByTestId('snapshot-restore-all-btn'))

    const status = await screen.findByTestId('snapshot-status')
    // A genuine RestoreError (stores rolled back) must surface as ERROR, not a
    // mild warning — even though its report carries a rebuiltButUnattached list.
    await waitFor(() => {
      expect(status.getAttribute('data-tone')).toBe('error')
    })
    // …and the disclosure (name/cwd/host) is still shown under the error.
    const item = screen.getByTestId('snapshot-unattached-item')
    expect(item.textContent).toContain('orphan-sess')
    expect(item.textContent).toContain('/srv/app')
    expect(item.textContent).toContain('h9')
    expect(warnSpy).toHaveBeenCalled()
    warnSpy.mockRestore()
  })

  it('clean success (empty rebuiltButUnattached) → success toast, no unattached list (F3)', async () => {
    const snap = snapWithData()
    mockedReadSnapshot.mockReturnValue(snap)
    mockedRestoreAll.mockResolvedValue({ reattached: 3, rebuilt: 0, failed: 0, rebuiltButUnattached: [] })

    render(<SnapshotSettingsSection />)
    fireEvent.click(screen.getByTestId('snapshot-restore-all-btn'))

    const status = await screen.findByTestId('snapshot-status')
    await waitFor(() => {
      expect(status.getAttribute('data-tone')).toBe('success')
    })
    expect(status.getAttribute('data-reattached')).toBe('3')
    expect(screen.queryByTestId('snapshot-unattached-item')).toBeNull()
  })

  it('capture on an empty page populates the section WITHOUT a remount (F1 refresh)', async () => {
    const populated = snapWithData()
    // Mounts empty; the post-capture refresh() re-reads storage and now finds a snapshot.
    mockedReadSnapshot.mockReturnValueOnce(null).mockReturnValue(populated)
    mockedCapture.mockResolvedValue({ total: 1, resolved: 1, unresolved: 0 })

    render(<SnapshotSettingsSection />)
    // Empty state: no restore/rebuild controls yet.
    expect(screen.getByTestId('snapshot-empty')).toBeTruthy()
    expect(screen.queryByTestId('snapshot-tmux-block')).toBeNull()

    fireEvent.click(screen.getByTestId('snapshot-capture-btn'))

    // Same component instance leaves empty state and reveals the restore controls.
    await waitFor(() => {
      expect(screen.queryByTestId('snapshot-empty')).toBeNull()
    })
    expect(screen.getByTestId('snapshot-tmux-block')).toBeTruthy()
    expect(screen.getByTestId('snapshot-restore-all-btn')).toBeTruthy()
    expect(screen.getByTestId('snapshot-rebuild-btn')).toBeTruthy()
  })

  it('after a restore resolves, health reconciliation re-runs for the host (F1 refresh)', async () => {
    // Fresh object each read so refresh() bumps the `snap` reference and re-fires the effect.
    mockedReadSnapshot.mockImplementation(() => snapWithData())

    render(<SnapshotSettingsSection />)
    // Initial mount: one reconciliation pass for h1.
    await waitFor(() => {
      expect(mockedListSessions).toHaveBeenCalledTimes(1)
    })

    fireEvent.click(screen.getByTestId('snapshot-restore-all-btn'))
    await waitFor(() => {
      expect(mockedRestoreAll).toHaveBeenCalledTimes(1)
    })
    // refresh() in runRestore's finally re-reconciles → listSessions('h1') again.
    await waitFor(() => {
      expect(mockedListSessions).toHaveBeenCalledTimes(2)
    })
    expect(mockedListSessions).toHaveBeenNthCalledWith(2, 'h1')
  })

  it('busy guard: two synchronous clicks fire the action only once', async () => {
    const snap = snapWithData()
    mockedReadSnapshot.mockReturnValue(snap)
    // Never-resolving promise keeps the single-flight guard latched.
    let release!: (r: RestoreReport) => void
    mockedRestoreAll.mockReturnValue(new Promise<RestoreReport>((r) => { release = r }))

    render(<SnapshotSettingsSection />)
    const btn = screen.getByTestId('snapshot-restore-all-btn')
    fireEvent.click(btn)
    fireEvent.click(btn)

    await waitFor(() => {
      expect(mockedRestoreAll).toHaveBeenCalledTimes(1)
    })
    release(EMPTY_REPORT)
  })
})

describe('SnapshotSettingsSection — shared operation lock (§4.11)', () => {
  it('a rebuild holding the lock disables every snapshot action', () => {
    mockedReadSnapshot.mockReturnValue(snapWithData())
    mockedReadPrev.mockReturnValue(snapWithData())
    useRebuildStore.setState({ lockedBy: 'rebuild:p1' })

    render(<SnapshotSettingsSection />)
    for (const id of [
      'snapshot-capture-btn',
      'snapshot-restore-all-btn',
      'snapshot-undo-btn',
      'snapshot-rebuild-btn',
      'snapshot-restore-tab-btn',
    ]) {
      expect(screen.getByTestId(id)).toBeDisabled()
    }
  })

  it('every snapshot action is enabled again once the lock is free', () => {
    mockedReadSnapshot.mockReturnValue(snapWithData())
    mockedReadPrev.mockReturnValue(snapWithData())

    render(<SnapshotSettingsSection />)
    for (const id of [
      'snapshot-capture-btn',
      'snapshot-restore-all-btn',
      'snapshot-undo-btn',
      'snapshot-rebuild-btn',
      'snapshot-restore-tab-btn',
    ]) {
      expect(screen.getByTestId(id)).toBeEnabled()
    }
  })

  it('a held lock also blocks an inline cwd edit', () => {
    mockedReadSnapshot.mockReturnValue(snapWithData())
    useRebuildStore.setState({ lockedBy: 'rebuild:p1' })

    render(<SnapshotSettingsSection />)
    fireEvent.doubleClick(screen.getByText('/x'))
    expect(screen.queryByDisplayValue('/x')).toBeNull()
    expect(mockedWriteSnapshot).not.toHaveBeenCalled()
  })
})

describe('SnapshotSettingsSection — inline cwd editing (T2)', () => {
  /**
   * Wire readSnapshot/writeSnapshot into a single mutable cell so a commit
   * persists and the subsequent refresh() re-reads the updated snapshot — mirrors
   * how the real storage boundary behaves so health reconciliation re-runs.
   */
  function seedStatefulSnapshot(initial: WorkspaceSnapshot) {
    let current: WorkspaceSnapshot | null = initial
    mockedReadSnapshot.mockImplementation(() => current)
    mockedWriteSnapshot.mockImplementation((s: WorkspaceSnapshot) => {
      current = s
    })
  }

  it('double-click a Directory cell → editable input pre-filled with the cwd', () => {
    mockedReadSnapshot.mockReturnValue(
      makeSnapshot({
        sessionMeta: { h1: { s1: meta({ hostId: 'h1', sessionCode: 's1', name: 'work', cwd: '/x' }) } },
      }),
    )

    render(<SnapshotSettingsSection />)
    fireEvent.doubleClick(screen.getByTestId('snapshot-cwd-cell'))
    expect((screen.getByTestId('snapshot-cwd-input') as HTMLInputElement).value).toBe('/x')
  })

  it('Enter commits → writeSnapshot called with the updated cwd', async () => {
    seedStatefulSnapshot(
      makeSnapshot({
        sessionMeta: { h1: { s1: meta({ hostId: 'h1', sessionCode: 's1', name: 'work', cwd: '/x' }) } },
      }),
    )

    render(<SnapshotSettingsSection />)
    fireEvent.doubleClick(screen.getByTestId('snapshot-cwd-cell'))
    const input = screen.getByTestId('snapshot-cwd-input')
    fireEvent.change(input, { target: { value: '/new/dir' } })
    fireEvent.keyDown(input, { key: 'Enter' })

    await waitFor(() => {
      expect(mockedWriteSnapshot).toHaveBeenCalledTimes(1)
    })
    const persisted = mockedWriteSnapshot.mock.calls[0][0]
    expect(persisted.sessionMeta.h1.s1.cwd).toBe('/new/dir')
    expect(persisted.sessionMeta.h1.s1.restorable).toBe(true)
    // The row now displays the persisted value after refresh().
    await waitFor(() => {
      expect(screen.getByTestId('snapshot-cwd-cell').textContent).toBe('/new/dir')
    })
  })

  it('Esc cancels → no writeSnapshot, cell reverts to the original value', () => {
    mockedReadSnapshot.mockReturnValue(
      makeSnapshot({
        sessionMeta: { h1: { s1: meta({ hostId: 'h1', sessionCode: 's1', name: 'work', cwd: '/orig' }) } },
      }),
    )

    render(<SnapshotSettingsSection />)
    fireEvent.doubleClick(screen.getByTestId('snapshot-cwd-cell'))
    const input = screen.getByTestId('snapshot-cwd-input')
    fireEvent.change(input, { target: { value: '/discarded' } })
    fireEvent.keyDown(input, { key: 'Escape' })
    fireEvent.blur(input)

    expect(mockedWriteSnapshot).not.toHaveBeenCalled()
    expect(screen.getByTestId('snapshot-cwd-cell').textContent).toBe('/orig')
  })

  it('editing a ⚠️ structure-only row to a real path → health flips to 🔴 dead-rebuildable', async () => {
    seedStatefulSnapshot(
      makeSnapshot({
        sessionMeta: {
          h1: { s1: meta({ hostId: 'h1', sessionCode: 's1', name: 'work', restorable: false, cwd: undefined }) },
        },
      }),
    )
    // Host reachable, session not in the live list → structure now, dead after cwd.
    mockedListSessions.mockResolvedValue([session({ code: 'other', name: 'other' })])

    render(<SnapshotSettingsSection />)
    await waitFor(() => {
      expect(screen.getByTestId('snapshot-health-h1-s1').getAttribute('data-health')).toBe('structure')
    })

    fireEvent.doubleClick(screen.getByTestId('snapshot-cwd-cell'))
    const input = screen.getByTestId('snapshot-cwd-input')
    fireEvent.change(input, { target: { value: '/real/dir' } })
    fireEvent.keyDown(input, { key: 'Enter' })

    await waitFor(() => {
      expect(screen.getByTestId('snapshot-health-h1-s1').getAttribute('data-health')).toBe('dead')
    })
  })

  it('while an action is in flight (busy) → double-click a Directory cell does NOT open the editor, no writeSnapshot', async () => {
    seedStatefulSnapshot(
      makeSnapshot({
        sessionMeta: { h1: { s1: meta({ hostId: 'h1', sessionCode: 's1', name: 'work', cwd: '/x' }) } },
      }),
    )
    // Never-resolving restore keeps `busy` (and busyRef) latched true.
    let release!: (r: RestoreReport) => void
    mockedRestoreAll.mockReturnValue(new Promise<RestoreReport>((r) => { release = r }))

    render(<SnapshotSettingsSection />)
    await waitFor(() => {
      expect(screen.getByTestId('snapshot-health-h1-s1')).toBeTruthy()
    })

    fireEvent.click(screen.getByTestId('snapshot-restore-all-btn'))
    // Confirm the section is actually busy (rebuild button disabled).
    await waitFor(() => {
      expect((screen.getByTestId('snapshot-rebuild-btn') as HTMLButtonElement).disabled).toBe(true)
    })

    // The cell is disabled → double-click must not enter edit mode.
    fireEvent.doubleClick(screen.getByTestId('snapshot-cwd-cell'))
    expect(screen.queryByTestId('snapshot-cwd-input')).toBeNull()
    expect(mockedWriteSnapshot).not.toHaveBeenCalled()

    release(EMPTY_REPORT)
  })

  it('committing an empty cwd → row becomes ⚠️ structure-only', async () => {
    seedStatefulSnapshot(
      makeSnapshot({
        sessionMeta: {
          h1: { s1: meta({ hostId: 'h1', sessionCode: 's1', name: 'work', restorable: true, cwd: '/x' }) },
        },
      }),
    )
    mockedListSessions.mockResolvedValue([session({ code: 'other', name: 'other' })])

    render(<SnapshotSettingsSection />)
    await waitFor(() => {
      expect(screen.getByTestId('snapshot-health-h1-s1').getAttribute('data-health')).toBe('dead')
    })

    fireEvent.doubleClick(screen.getByTestId('snapshot-cwd-cell'))
    const input = screen.getByTestId('snapshot-cwd-input')
    fireEvent.change(input, { target: { value: '   ' } })
    fireEvent.keyDown(input, { key: 'Enter' })

    await waitFor(() => {
      expect(screen.getByTestId('snapshot-health-h1-s1').getAttribute('data-health')).toBe('structure')
    })
    expect(mockedWriteSnapshot.mock.calls[0][0].sessionMeta.h1.s1.cwd).toBeUndefined()
  })
})
