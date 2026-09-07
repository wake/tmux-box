// spa/src/components/settings/SnapshotSettingsSection.records.test.tsx
//
// Task 16 — the per-tab rebuild records table and "Rebuild all" (spec §4.11).
//
// The records block reads `useTabStore` directly rather than the captured
// snapshot: it is a view over the live per-tab records, so it renders with or
// without a snapshot. `runBatchRebuild` / `rebuildPane` are stubbed through
// partial mocks so the grouping and the conflict rendering stay real.
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { SnapshotSettingsSection } from './SnapshotSettingsSection'
import { useRebuildStore } from '../../stores/useRebuildStore'
import { useTabStore } from '../../stores/useTabStore'
import * as storageModule from '../../lib/snapshot/storage'
import * as hostApiModule from '../../lib/host-api'
import * as batchModule from '../../lib/rebuild/batch'
import * as engineModule from '../../lib/rebuild/engine'
import type { Session } from '../../lib/host-api'
import type { WorkspaceSnapshot } from '../../lib/snapshot/types'
import type { Tab, TmuxSessionContent } from '../../types/tab'

vi.mock('../../lib/snapshot/storage', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../lib/snapshot/storage')>()),
  readSnapshot: vi.fn(),
  writeSnapshot: vi.fn(),
  readPrevSnapshot: vi.fn(),
  writePrevSnapshot: vi.fn(),
}))
vi.mock('../../lib/host-api')
vi.mock('../../lib/snapshot/capture')
vi.mock('../../lib/snapshot/restore')
// Partial mocks: only the two actions that talk to a daemon are replaced.
vi.mock('../../lib/rebuild/batch', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../lib/rebuild/batch')>()),
  runBatchRebuild: vi.fn(),
}))
vi.mock('../../lib/rebuild/engine', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../lib/rebuild/engine')>()),
  rebuildPane: vi.fn(),
}))

const mockedReadSnapshot = vi.mocked(storageModule.readSnapshot)
const mockedReadPrev = vi.mocked(storageModule.readPrevSnapshot)
const mockedListSessions = vi.mocked(hostApiModule.listSessions)
const mockedRunBatch = vi.mocked(batchModule.runBatchRebuild)
const mockedRebuildPane = vi.mocked(engineModule.rebuildPane)

function session(over: Partial<Session> & Pick<Session, 'code' | 'name'>): Session {
  return { cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false, ...over }
}

function recordTab(tabId: string, paneId: string, over: Partial<TmuxSessionContent> = {}): Tab {
  const base: TmuxSessionContent = {
    kind: 'tmux-session',
    hostId: 'h1',
    sessionCode: 'old111',
    mode: 'terminal',
    cachedName: 'dev',
    tmuxInstance: '111:1000',
    terminated: 'tmux-restarted',
    rebuild: { sessionName: 'dev', tmuxInstance: '111:1000', cwd: '/w', capturedAt: 1 },
  }
  return {
    id: tabId, pinned: false, locked: false, createdAt: 0,
    layout: { type: 'leaf', pane: { id: paneId, content: { ...base, ...over } } },
  }
}

function seedTabs(...tabs: Tab[]) {
  useTabStore.setState({
    tabs: Object.fromEntries(tabs.map((tab) => [tab.id, tab])),
    tabOrder: tabs.map((tab) => tab.id),
    activeTabId: tabs[0]?.id ?? null,
  })
}

/** A captured snapshot, only needed by the legacy-labelling case. */
function snapWithData(): WorkspaceSnapshot {
  return {
    version: 1, capturedAt: Date.now(), tabs: {}, tabOrder: [], activeTabId: null,
    workspaces: [], activeWorkspaceId: null,
    sessionMeta: { h1: { s1: { hostId: 'h1', sessionCode: 's1', name: 'work', mode: 'terminal', restorable: true, cwd: '/x' } } },
  }
}

describe('SnapshotSettingsSection — per-tab rebuild records (T16)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useRebuildStore.setState({ operations: {}, lockedBy: null })
    mockedReadSnapshot.mockReturnValue(null)
    mockedReadPrev.mockReturnValue(null)
    mockedListSessions.mockResolvedValue([])
    mockedRunBatch.mockResolvedValue({ status: 'ok', groups: [], excluded: [] })
    mockedRebuildPane.mockResolvedValue({
      hostId: 'h1',
      steps: { create: { status: 'ok' }, resume: { status: 'skipped' }, repoint: { status: 'ok' } },
      repointed: true,
    })
    seedTabs()
  })

  it('renders one row per terminal tmux pane, with the section four-state health indicator', async () => {
    seedTabs(
      recordTab('t1', 'p1'),
      recordTab('t2', 'p2', { sessionCode: 'live1', cachedName: 'alive', terminated: undefined,
        rebuild: { sessionName: 'alive', tmuxInstance: '111:1000', cwd: '/w', capturedAt: 1 } }),
    )
    mockedListSessions.mockResolvedValue([session({ code: 'live1', name: 'alive' })])

    render(<SnapshotSettingsSection />)
    await waitFor(() => {
      expect(screen.getByTestId('record-health-p1').getAttribute('data-health')).toBe('dead')
    })
    expect(screen.getByTestId('record-health-p2').getAttribute('data-health')).toBe('live')
    // The same labels the captured-snapshot table above it uses.
    expect(screen.getByTestId('record-health-p1').textContent).toBe('Rebuildable')
  })

  it('an unreachable host greys every record row out, exactly as the snapshot table does', async () => {
    seedTabs(recordTab('t1', 'p1'))
    mockedListSessions.mockRejectedValue(new Error('offline'))

    render(<SnapshotSettingsSection />)
    await waitFor(() => {
      expect(screen.getByTestId('record-health-p1').getAttribute('data-health')).toBe('offline')
    })
  })

  it('a record with no cwd is structure-only, never rebuildable', async () => {
    seedTabs(recordTab('t1', 'p1', {
      rebuild: { sessionName: 'dev', tmuxInstance: '111:1000', capturedAt: 1 },
    }))
    render(<SnapshotSettingsSection />)
    await waitFor(() => {
      expect(screen.getByTestId('record-health-p1').getAttribute('data-health')).toBe('structure')
    })
  })

  it('names the winning pane before running when hand-edits conflict', async () => {
    seedTabs(
      recordTab('t1', 'p1', { rebuild: { sessionName: 'dev', tmuxInstance: '111:1000', cwd: '/a', capturedAt: 1 } }),
      recordTab('t2', 'p2', { rebuild: { sessionName: 'dev', tmuxInstance: '111:1000', cwd: '/b', capturedAt: 9 } }),
    )
    render(<SnapshotSettingsSection />)
    const conflict = await screen.findByTestId('batch-conflict-source')
    expect(conflict.textContent).toContain('p2')
    expect(conflict.textContent).toContain('/b')
  })

  it('says nothing about conflicts when the group agrees', () => {
    seedTabs(recordTab('t1', 'p1'), recordTab('t2', 'p2'))
    render(<SnapshotSettingsSection />)
    expect(screen.queryByTestId('batch-conflict-source')).toBeNull()
  })

  it('"Rebuild all" runs the batch once and reports what it did', async () => {
    seedTabs(recordTab('t1', 'p1'), recordTab('t2', 'p2'))
    mockedRunBatch.mockResolvedValue({
      status: 'ok',
      excluded: [],
      groups: [{
        sourcePaneId: 'p1', hostId: 'h1', sessionCode: 'old111', tmuxInstance: '111:1000',
        report: {
          hostId: 'h1',
          created: { code: 'new1', name: 'dev', tmuxInstance: '222:2000' },
          steps: { create: { status: 'ok' }, resume: { status: 'ok' }, repoint: { status: 'ok' } },
          repointed: true,
        },
        members: [{ paneId: 'p2', tabId: 't2', repointed: true }],
      }],
    })

    render(<SnapshotSettingsSection />)
    fireEvent.click(screen.getByTestId('record-rebuild-all-btn'))

    await waitFor(() => {
      expect(mockedRunBatch).toHaveBeenCalledTimes(1)
    })
    await waitFor(() => {
      expect(screen.getByTestId('snapshot-status').getAttribute('data-tone')).toBe('success')
    })
  })

  it('lists an unknown-generation pane under "needs attention" with its own Rebuild', async () => {
    seedTabs(recordTab('t1', 'p1'), recordTab('t2', 'p2', { tmuxInstance: '' }))
    render(<SnapshotSettingsSection />)

    // Excluded from the automatic batch…
    expect(screen.queryByTestId('record-attention-rebuild-p1')).toBeNull()
    // …and offered one at a time instead.
    fireEvent.click(screen.getByTestId('record-attention-rebuild-p2'))
    await waitFor(() => {
      expect(mockedRebuildPane).toHaveBeenCalledTimes(1)
    })
    expect(mockedRebuildPane.mock.calls[0].slice(0, 3)).toEqual(['h1', 't2', 'p2'])
  })

  it('labels the legacy snapshot actions shell-only', () => {
    mockedReadSnapshot.mockReturnValue(snapWithData())
    render(<SnapshotSettingsSection />)
    expect(screen.getByTestId('snapshot-legacy-shell-only').textContent).toMatch(/shell/i)
  })

  it('disables "Rebuild all" while another owner holds the operation lock', () => {
    seedTabs(recordTab('t1', 'p1'))
    useRebuildStore.getState().acquireOperationLock('rebuild:p9')
    render(<SnapshotSettingsSection />)
    expect((screen.getByTestId('record-rebuild-all-btn') as HTMLButtonElement).disabled).toBe(true)
  })

  it('a blocked batch reports who is holding the lock instead of failing silently', async () => {
    seedTabs(recordTab('t1', 'p1'))
    mockedRunBatch.mockResolvedValue({ status: 'blocked', blockedBy: 'rebuild:p9', groups: [], excluded: [] })
    render(<SnapshotSettingsSection />)
    fireEvent.click(screen.getByTestId('record-rebuild-all-btn'))
    await waitFor(() => {
      expect(screen.getByTestId('snapshot-status').getAttribute('data-tone')).toBe('warn')
    })
    expect(screen.getByTestId('snapshot-status').textContent).toContain('rebuild:p9')
  })
})
