import { useEffect, useMemo, useRef, useState } from 'react'
import {
  ArrowCounterClockwise,
  ArrowsClockwise,
  Camera,
  CheckCircle,
  CircleDashed,
  CircleNotch,
  Warning,
  WarningCircle,
} from '@phosphor-icons/react'
import { SettingItem } from './SettingItem'
import { EditableCwdCell } from './EditableCwdCell'
import { useI18nStore } from '../../stores/useI18nStore'
import { readPrevSnapshot, readSnapshot, setSessionMetaCwd, writeSnapshot } from '../../lib/snapshot/storage'
import { captureSnapshot } from '../../lib/snapshot/capture'
import {
  rebuildAllSessions,
  restoreAll,
  restoreTabLayout,
  undoLastRestore,
  SNAPSHOT_LOCK_OWNER,
} from '../../lib/snapshot/restore'
import { useRebuildStore } from '../../stores/useRebuildStore'
import { useTabStore } from '../../stores/useTabStore'
import {
  BATCH_LOCK_OWNER,
  collectRecordRows,
  groupForBatch,
  batchCandidates,
  planForRecord,
  recordsDisagree,
  runBatchRebuild,
  type BatchCandidate,
  type BatchGroup,
  type RecordRow,
} from '../../lib/rebuild/batch'
import { rebuildPane } from '../../lib/rebuild/engine'
import { RestoreError } from '../../lib/snapshot/types'
import type { RestoreReport, SessionMeta, WorkspaceSnapshot } from '../../lib/snapshot/types'
import { listSessions } from '../../lib/host-api'
import type { Session } from '../../lib/host-api'
import { collectLeaves } from '../../lib/pane-tree'
import type { PaneContent } from '../../types/tab'

type Tone = 'idle' | 'busy' | 'success' | 'warn' | 'error'

interface Status {
  tone: Tone
  message: string
  /** data-* attributes exposed for tests / debugging (counts, totals). */
  attrs?: Record<string, string | number>
  /** Rebuilt-but-unattached sessions to disclose (R3 B), name/cwd/host each. */
  unattached?: RestoreReport['rebuiltButUnattached']
}

const IDLE: Status = { tone: 'idle', message: '' }

/**
 * Capture never takes the operation lock — it creates nothing — but it must not
 * photograph a half-rebuilt world either, so it carries an owner purely so the
 * "is anyone else holding the lock" test disables it like the rest.
 */
const CAPTURE_OWNER = 'snapshot:capture'

function errMessage(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

/** Per-host live-session lookup state: still loading, host offline, or the list. */
type HostLive = 'loading' | 'offline' | Session[]

/** Four-state (plus loading) health of a captured session vs. the live world. */
type Health = 'loading' | 'live' | 'dead' | 'structure' | 'offline'

/**
 * Reconcile one captured {@link SessionMeta} against its host's live list. Mirrors
 * the engine's reattach rule (code AND name must match — a code-only match is a
 * different session after a tmux restart, so it is NOT "live").
 *
 * Precedence: an unreachable host wins (every row ⚪) → a live code+name match is
 * 🟢 → a non-restorable OR cwd-less entry is ⚠️ (the engine's `ensureSessions`
 * refuses to rebuild without a cwd, so it can never be 🔴) → otherwise 🔴
 * (restorable + cwd + dead, so "Rebuild all" can actually recreate it).
 */
function computeHealth(meta: SessionMeta, live: HostLive): Health {
  if (live === 'loading') return 'loading'
  if (live === 'offline') return 'offline'
  const match = live.some((s) => s.code === meta.sessionCode && s.name === meta.name)
  if (match) return 'live'
  if (!meta.restorable || !meta.cwd) return 'structure'
  return 'dead'
}

/**
 * The same four states over a per-tab rebuild record (spec §4.11). Deliberately
 * the same precedence as {@link computeHealth} — an unreachable host wins, then
 * a live code+name match, then "we could not put it back where it was" — so the
 * two tables in this section never disagree about what a colour means.
 *
 * ⚠️ here is a record with no cwd: Rebuild would open a shell in the wrong
 * directory, which is exactly the "structure only" the snapshot table means.
 */
function computeRecordHealth(row: RecordRow, live: HostLive): Health {
  if (live === 'loading') return 'loading'
  if (live === 'offline') return 'offline'
  const name = row.record?.sessionName || row.cachedName
  if (live.some((s) => s.code === row.sessionCode && s.name === name)) return 'live'
  if (!row.record?.cwd) return 'structure'
  return 'dead'
}

const HEALTH_ICON: Record<Health, typeof CheckCircle> = {
  loading: CircleNotch,
  live: CheckCircle,
  dead: WarningCircle,
  structure: Warning,
  offline: CircleDashed,
}

const HEALTH_COLOR: Record<Health, string> = {
  loading: 'text-text-muted',
  live: 'text-green-500',
  dead: 'text-red-500',
  structure: 'text-yellow-500',
  offline: 'text-text-muted',
}

/**
 * The section's one health indicator, so the captured-snapshot table and the
 * per-tab records table cannot drift apart in icon, colour or wording.
 */
function HealthBadge({
  health,
  testId,
  t,
}: {
  health: Health
  testId: string
  t: ReturnType<typeof useI18nStore.getState>['t']
}) {
  const Icon = HEALTH_ICON[health]
  return (
    <span
      data-testid={testId}
      data-health={health}
      title={t(`settings.snapshot.health.${health}`)}
      className={`inline-flex items-center gap-1 ${HEALTH_COLOR[health]}`}
    >
      <Icon size={14} className={health === 'loading' ? 'animate-spin' : ''} />
      {t(`settings.snapshot.health.${health}`)}
    </span>
  )
}

/** Human label for a leaf pane, used in the Tabs tree (block 2). */
function leafLabel(content: PaneContent): string {
  switch (content.kind) {
    case 'tmux-session':
      return content.cachedName || content.sessionCode
    case 'editor':
    case 'image-preview':
    case 'pdf-preview':
      return content.filePath
    case 'browser':
      return content.url
    default:
      return content.kind
  }
}

function formatRelativeTime(t: ReturnType<typeof useI18nStore.getState>['t'], ms: number): string {
  const diffSec = Math.floor((Date.now() - ms) / 1000)
  if (diffSec < 60) return t('settings.snapshot.time.secondsAgo', { n: diffSec })
  const diffMin = Math.floor(diffSec / 60)
  if (diffMin < 60) return t('settings.snapshot.time.minutesAgo', { n: diffMin })
  const diffHr = Math.floor(diffMin / 60)
  if (diffHr < 24) return t('settings.snapshot.time.hoursAgo', { n: diffHr })
  const diffDay = Math.floor(diffHr / 24)
  return t('settings.snapshot.time.daysAgo', { n: diffDay })
}

export function SnapshotSettingsSection() {
  const t = useI18nStore((s) => s.t)
  const [snap, setSnap] = useState<WorkspaceSnapshot | null>(() => readSnapshot())
  const [busy, setBusy] = useState(false)
  // Single-flight guard as a ref (not state) so two synchronous clicks — which
  // share one render's `busy` closure — still only fire one action. This is the
  // component's own guard and stays: it also covers Capture and the cwd edit,
  // neither of which takes the global lock below.
  const busyRef = useRef(false)
  // The GLOBAL operation lock (spec §4.11). A Tab Rebuild takes it too, so a
  // legacy restore must not be reachable while one is in flight — every button
  // whose owner is not the current holder is disabled.
  const lockedBy = useRebuildStore((s) => s.lockedBy)
  const lockedOut = (owner: string) => lockedBy !== null && lockedBy !== owner
  const [status, setStatus] = useState<Status>(IDLE)
  // Read fresh each render: setBusy re-renders after every action, so once a
  // restore writes the `-prev` backup the Undo button re-enables on its own.
  const hasPrev = readPrevSnapshot() !== null
  // Seed every captured host to 'loading' up front (lazy init) so the effect
  // only ever calls setState asynchronously from the resolve/reject callbacks.
  const [liveByHost, setLiveByHost] = useState<Record<string, HostLive>>(() =>
    snap
      ? Object.fromEntries(Object.keys(snap.sessionMeta).map((h) => [h, 'loading' as HostLive]))
      : {},
  )

  // Per snapshot, reconcile each captured host exactly once against its live
  // session list. A rejection means the host is offline → every row for that host
  // renders ⚪ and no createSession is ever attempted here. Depends on `snap`, and
  // `refresh()` yields a NEW snapshot reference, so a capture/restore/undo re-runs
  // this reconciliation (not just the first mount). At the START we re-seed the
  // current snapshot's hosts to 'loading' so a re-run drops stale per-host results;
  // every subsequent setState happens in an async callback guarded by `cancelled`,
  // so there is never a setState-after-unmount.
  // The per-tab rebuild records (spec §4.11) — a view over the LIVE tab tree,
  // not over the captured snapshot, so it renders whether or not a snapshot
  // exists. `tabs` is the store's own object reference: it changes only when a
  // tab does, which is exactly when the rows have to be recomputed.
  const tabs = useTabStore((s) => s.tabs)
  const recordRows = useMemo(() => collectRecordRows(tabs), [tabs])
  const { groups: recordGroups, excluded: recordExcluded } = useMemo(
    () => groupForBatch(batchCandidates(recordRows)),
    [recordRows],
  )
  // A stable primitive so the reconciliation effect below re-runs when the SET
  // of hosts changes, not on every unrelated tab edit.
  const recordHostKey = useMemo(
    () => Array.from(new Set(recordRows.map((r) => r.hostId))).sort().join(','),
    [recordRows],
  )

  useEffect(() => {
    const recordHosts = recordHostKey ? recordHostKey.split(',') : []
    const hostIds = Array.from(new Set([...Object.keys(snap?.sessionMeta ?? {}), ...recordHosts]))
    if (hostIds.length === 0) return
    setLiveByHost(Object.fromEntries(hostIds.map((h) => [h, 'loading' as HostLive])))
    let cancelled = false
    for (const hostId of hostIds) {
      listSessions(hostId)
        .then((sessions) => {
          if (!cancelled) setLiveByHost((prev) => ({ ...prev, [hostId]: sessions }))
        })
        .catch(() => {
          if (!cancelled) setLiveByHost((prev) => ({ ...prev, [hostId]: 'offline' }))
        })
    }
    return () => {
      cancelled = true
    }
  }, [snap, recordHostKey])

  // Re-read the persisted snapshot into state. Because storage returns a fresh
  // object, this bumps the `snap` reference → the reconciliation effect re-fires
  // and the restore handlers close over the latest snapshot on their next click.
  const refresh = () => setSnap(readSnapshot())

  /**
   * The residual window the `disabled` props cannot cover: the lock can be
   * taken between the last render and this click. Read it fresh and say why
   * nothing happened, rather than firing an action that `restore.ts` would
   * refuse — by throwing — one frame later.
   */
  const refuseWhileLocked = (owner: string): boolean => {
    const holder = useRebuildStore.getState().lockedBy
    if (holder === null || holder === owner) return false
    setStatus({ tone: 'warn', message: t('settings.snapshot.toast.locked', { owner: holder }) })
    return true
  }

  // Persist a manual cwd edit for one session, then re-read + re-reconcile via
  // `refresh()` so the row's health badge reflects the new restorable state. Pure
  // client-side: no daemon call, live sessions untouched.
  const handleCommitCwd = (hostId: string, code: string, value: string) => {
    // Reject edits landing while a capture/restore/rebuild action is in flight:
    // that action closed over the pre-edit snapshot and its result would silently
    // overwrite this write. (The cell is also `disabled` while busy so a row
    // normally cannot even enter edit mode — this guards the residual window.)
    if (busyRef.current) return
    // Same reasoning for the global lock: a rebuild re-points panes and the
    // snapshot this edit writes into would be stale the moment it lands.
    if (useRebuildStore.getState().lockedBy !== null) return
    const cur = readSnapshot()
    if (!cur) return
    writeSnapshot(setSessionMetaCwd(cur, hostId, code, value))
    refresh()
  }

  const handleCapture = async () => {
    if (busyRef.current) return
    if (refuseWhileLocked(CAPTURE_OWNER)) return
    busyRef.current = true
    setBusy(true)
    setStatus({ tone: 'busy', message: t('settings.snapshot.toast.capturing') })
    try {
      // Only the UI layer reads the clock — the engine takes `now` as a param.
      const res = await captureSnapshot(Date.now())
      setStatus({
        tone: 'success',
        message: t('settings.snapshot.toast.captured', { total: res.total, unresolved: res.unresolved }),
        attrs: { 'data-total': res.total, 'data-unresolved': res.unresolved },
      })
      // Pull the just-written snapshot into state: empty→populated transition,
      // fresh capturedAt, and a re-reconciled health table.
      refresh()
    } catch (e) {
      setStatus({ tone: 'error', message: t('settings.snapshot.toast.captureFailed', { reason: errMessage(e) }) })
    } finally {
      busyRef.current = false
      setBusy(false)
    }
  }

  // Turn a resolved/collected RestoreReport into a status. `failed` (a RestoreError,
  // stores rolled back) takes ERROR-tone precedence: the engine only ever fills
  // rebuiltButUnattached on the failure path, so a non-empty list must NOT be
  // downgraded to a mild warning. The disclosure (name/cwd/host) is always logged
  // via console.warn and shown under the status, whichever tone wins.
  const reportStatus = (report: RestoreReport, failed: boolean) => {
    const attrs = {
      'data-reattached': report.reattached,
      'data-rebuilt': report.rebuilt,
      'data-failed': report.failed,
    }
    const unattached = report.rebuiltButUnattached
    if (unattached.length > 0) {
      console.warn('[snapshot] sessions rebuilt but could not be reattached', unattached)
    }
    if (failed) {
      setStatus({
        tone: 'error',
        message: t('settings.snapshot.toast.restoreError'),
        attrs,
        unattached: unattached.length > 0 ? unattached : undefined,
      })
    } else if (unattached.length > 0) {
      // Success-with-unattached — defensive only; the engine currently returns
      // an empty list on every success path.
      setStatus({
        tone: 'warn',
        message: t('settings.snapshot.toast.rebuiltUnattached'),
        attrs,
        unattached,
      })
    } else {
      setStatus({
        tone: 'success',
        message: t('settings.snapshot.toast.restoreReport', {
          reattached: report.reattached,
          rebuilt: report.rebuilt,
          failed: report.failed,
        }),
        attrs,
      })
    }
  }

  const runRestore = async (owner: string, action: () => Promise<RestoreReport | null>) => {
    if (busyRef.current) return
    if (refuseWhileLocked(owner)) return
    busyRef.current = true
    setBusy(true)
    setStatus({ tone: 'busy', message: t('settings.snapshot.toast.restoring') })
    try {
      const report = await action()
      if (report === null) {
        // undoLastRestore with no `-prev` backup — nothing happened.
        setStatus({ tone: 'warn', message: t('settings.snapshot.toast.undoNothing') })
        return
      }
      reportStatus(report, false)
    } catch (e) {
      if (e instanceof RestoreError) {
        reportStatus(e.report, true)
      } else {
        setStatus({ tone: 'error', message: t('settings.snapshot.toast.restoreFailed', { reason: errMessage(e) }) })
      }
    } finally {
      busyRef.current = false
      setBusy(false)
      // Restore/rebuild/undo mutate live sessions and the `-prev` backup, so the
      // health table must re-reconcile and the handlers must use the latest snapshot.
      refresh()
    }
  }

  const handleRebuild = () =>
    snap && runRestore(SNAPSHOT_LOCK_OWNER.rebuildAll, () => rebuildAllSessions(snap))
  const handleRestoreTab = () =>
    snap && runRestore(SNAPSHOT_LOCK_OWNER.restoreLayout, () => restoreTabLayout(snap))
  // "Restore everything" — the one entry that both rebuilds and replaces the
  // tab tree, and therefore the one a Tab Rebuild most needs locking out.
  const handleRestoreAll = () =>
    snap && runRestore(SNAPSHOT_LOCK_OWNER.restoreAll, () => restoreAll(snap))
  const handleUndo = () => runRestore(SNAPSHOT_LOCK_OWNER.undo, () => undoLastRestore())

  /**
   * The two entry points into the new engine. Both go through the SAME
   * single-flight ref and the same lock as the legacy actions above — a batch
   * and a legacy restore may never interleave (spec §4.11) — and neither
   * touches the captured snapshot, so no `refresh()` is needed afterwards.
   */
  const runRebuildAction = async (owner: string, action: () => Promise<Status>) => {
    if (busyRef.current) return
    if (refuseWhileLocked(owner)) return
    busyRef.current = true
    setBusy(true)
    setStatus({ tone: 'busy', message: t('rebuild.batch_running') })
    try {
      setStatus(await action())
    } catch (e) {
      setStatus({ tone: 'error', message: t('settings.snapshot.toast.restoreFailed', { reason: errMessage(e) }) })
    } finally {
      busyRef.current = false
      setBusy(false)
    }
  }

  const handleRebuildAll = () =>
    runRebuildAction(BATCH_LOCK_OWNER, async () => {
      const report = await runBatchRebuild()
      if (report.status === 'blocked') {
        return { tone: 'warn', message: t('settings.snapshot.toast.locked', { owner: report.blockedBy ?? '' }) }
      }
      const created = report.groups.filter((g) => g.report.created).length
      // Every pane that ended up on a new session: the group's own source plus
      // each member that still matched its binding.
      const repointed = report.groups.reduce(
        (n, g) => n + (g.report.repointed ? 1 : 0) + g.members.filter((m) => m.repointed).length,
        0,
      )
      return {
        tone: created === report.groups.length ? 'success' : 'warn',
        message: t('rebuild.batch_report', { created, total: report.groups.length, repointed }),
        attrs: { 'data-groups': report.groups.length, 'data-created': created, 'data-repointed': repointed },
      }
    })

  const handleRebuildOne = (pane: BatchCandidate) =>
    runRebuildAction(`rebuild:${pane.paneId}`, async () => {
      const report = await rebuildPane(pane.hostId, pane.tabId, pane.paneId, planForRecord(pane.record))
      if (report.steps.create.status === 'failed') {
        return { tone: 'error', message: report.steps.create.error ?? t('settings.snapshot.toast.restoreError') }
      }
      return {
        tone: report.repointed ? 'success' : 'warn',
        message: t('rebuild.batch_report', { created: 1, total: 1, repointed: report.repointed ? 1 : 0 }),
      }
    })

  return (
    <div>
      <h2 className="text-lg text-text-primary">{t('settings.section.snapshot')}</h2>
      <p className="text-xs text-text-secondary mb-6">{t('settings.snapshot.description')}</p>

      <SettingItem
        label={t('settings.snapshot.capture')}
        description={
          snap
            ? t('settings.snapshot.capturedAt', { time: formatRelativeTime(t, snap.capturedAt) })
            : t('settings.snapshot.neverCaptured')
        }
      >
        <button
          type="button"
          data-testid="snapshot-capture-btn"
          onClick={handleCapture}
          disabled={busy || lockedOut(CAPTURE_OWNER)}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-border-default text-text-secondary text-xs hover:text-text-primary hover:border-border-active disabled:opacity-50 disabled:cursor-not-allowed"
        >
          <Camera size={14} className={busy ? 'animate-pulse' : ''} />
          {t('settings.snapshot.capture')}
        </button>
      </SettingItem>

      <RebuildRecordsBlock
        rows={recordRows}
        groups={recordGroups}
        excluded={recordExcluded}
        liveByHost={liveByHost}
        busy={busy || lockedOut(BATCH_LOCK_OWNER)}
        lockedOut={lockedOut}
        onRebuildAll={handleRebuildAll}
        onRebuildOne={handleRebuildOne}
        t={t}
      />

      {!snap ? (
        <p data-testid="snapshot-empty" className="text-xs text-text-muted mt-4">
          {t('settings.snapshot.empty')}
        </p>
      ) : (
        <>
          {/* Spec §4.11 capability table: none of the legacy actions below ever
              runs an agent command — they recreate the session as a bare shell. */}
          <p data-testid="snapshot-legacy-shell-only" className="mt-6 text-xs text-status-warning">
            {t('rebuild.legacy_shell_only')}
          </p>

          <SettingItem
            label={t('settings.snapshot.restore.label')}
            description={t('settings.snapshot.restore.description')}
          >
            <div className="flex items-center gap-2">
              <button
                type="button"
                data-testid="snapshot-restore-all-btn"
                onClick={handleRestoreAll}
                disabled={busy || lockedOut(SNAPSHOT_LOCK_OWNER.restoreAll)}
                className="flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-border-default text-text-secondary text-xs hover:text-text-primary hover:border-border-active disabled:opacity-50 disabled:cursor-not-allowed"
              >
                <ArrowsClockwise size={14} />
                {t('settings.snapshot.restore.all')}
              </button>
              <button
                type="button"
                data-testid="snapshot-undo-btn"
                onClick={handleUndo}
                disabled={busy || !hasPrev || lockedOut(SNAPSHOT_LOCK_OWNER.undo)}
                className="flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-border-default text-text-secondary text-xs hover:text-text-primary hover:border-border-active disabled:opacity-50 disabled:cursor-not-allowed"
              >
                <ArrowCounterClockwise size={14} />
                {t('settings.snapshot.restore.undo')}
              </button>
            </div>
          </SettingItem>

          <TmuxBlock
            snap={snap}
            liveByHost={liveByHost}
            busy={busy || lockedOut(SNAPSHOT_LOCK_OWNER.rebuildAll)}
            onRebuild={handleRebuild}
            onCommitCwd={handleCommitCwd}
            t={t}
          />
          <TabsBlock
            snap={snap}
            busy={busy || lockedOut(SNAPSHOT_LOCK_OWNER.restoreLayout)}
            onRestoreLayout={handleRestoreTab}
            t={t}
          />
        </>
      )}

      <StatusLine status={status} />
    </div>
  )
}

function StatusLine({ status }: { status: Status }) {
  if (status.tone === 'idle' || !status.message) return null
  const Icon =
    status.tone === 'success' ? CheckCircle
    : status.tone === 'warn' ? Warning
    : status.tone === 'error' ? WarningCircle
    : CircleNotch
  const color =
    status.tone === 'success' ? 'text-green-500'
    : status.tone === 'warn' ? 'text-yellow-500'
    : status.tone === 'error' ? 'text-red-500'
    : 'text-text-secondary'
  return (
    <div
      data-testid="snapshot-status"
      data-tone={status.tone}
      {...status.attrs}
      className={`mt-4 text-xs ${color}`}
    >
      <div className="flex items-start gap-1.5">
        <Icon size={14} className={status.tone === 'busy' ? 'animate-spin mt-0.5' : 'mt-0.5'} />
        <span>{status.message}</span>
      </div>
      {status.unattached && status.unattached.length > 0 && (
        <ul className="mt-1 ml-5 flex flex-col gap-0.5 font-mono">
          {status.unattached.map((s, i) => (
            // name/cwd/host are runtime data (not i18n) — render literally so the
            // disclosure is always legible regardless of the active locale.
            <li key={`${s.hostId}:${s.name}:${i}`} data-testid="snapshot-unattached-item">
              {s.name} · {s.cwd} · {s.hostId}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

/**
 * The per-tab rebuild records (spec §4.11): one row per terminal tmux pane,
 * the four-state health indicator this section already uses, "Rebuild all"
 * over the grouped dead ones, and — separately — the panes whose generation is
 * unknown, which the batch deliberately refuses to group by code alone.
 */
function RebuildRecordsBlock({
  rows,
  groups,
  excluded,
  liveByHost,
  busy,
  lockedOut,
  onRebuildAll,
  onRebuildOne,
  t,
}: {
  rows: RecordRow[]
  groups: BatchGroup[]
  excluded: BatchCandidate[]
  liveByHost: Record<string, HostLive>
  busy: boolean
  lockedOut: (owner: string) => boolean
  onRebuildAll: () => void
  onRebuildOne: (pane: BatchCandidate) => void
  t: ReturnType<typeof useI18nStore.getState>['t']
}) {
  // Only the groups whose members disagree need naming — saying "using p1"
  // when there is nothing to choose between is noise.
  const conflicts = groups.filter((group) => {
    const members = group.paneIds
      .map((paneId) => rows.find((r) => r.paneId === paneId)?.record)
      .filter((record): record is NonNullable<typeof record> => !!record)
    return members.some((record) => recordsDisagree(record, group.record))
  })

  return (
    <div data-testid="rebuild-records-block" className="mt-6">
      <div className="flex items-center justify-between mb-2">
        <h3 className="text-sm text-text-primary">{t('rebuild.batch_title')}</h3>
        <button
          type="button"
          data-testid="record-rebuild-all-btn"
          onClick={onRebuildAll}
          disabled={busy || groups.length === 0}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-border-default text-text-secondary text-xs hover:text-text-primary hover:border-border-active disabled:opacity-50 disabled:cursor-not-allowed"
        >
          <ArrowsClockwise size={14} />
          {t('rebuild.batch_run')}
        </button>
      </div>

      {rows.length === 0 ? (
        <p data-testid="rebuild-records-none" className="text-xs text-text-muted">{t('rebuild.batch_none')}</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-xs text-text-secondary">
            <thead>
              <tr className="text-text-muted text-left">
                <th className="py-1 pr-3 font-normal">{t('settings.snapshot.col.host')}</th>
                <th className="py-1 pr-3 font-normal">{t('settings.snapshot.col.name')}</th>
                <th className="py-1 pr-3 font-normal">{t('settings.snapshot.col.cwd')}</th>
                <th className="py-1 pr-3 font-normal">{t('settings.snapshot.col.command')}</th>
                <th className="py-1 font-normal">{t('settings.snapshot.col.health')}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => (
                <tr key={row.paneId} className="border-t border-border-default">
                  <td className="py-1 pr-3 text-text-primary">{row.hostId}</td>
                  <td className="py-1 pr-3">{row.record?.sessionName || row.cachedName}</td>
                  <td className="py-1 pr-3 font-mono">{row.record?.cwd || '—'}</td>
                  <td className="py-1 pr-3 font-mono">{row.record?.resumeCommand || '—'}</td>
                  <td className="py-1">
                    <HealthBadge
                      health={computeRecordHealth(row, liveByHost[row.hostId] ?? 'loading')}
                      testId={`record-health-${row.paneId}`}
                      t={t}
                    />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {conflicts.map((group) => (
        <p
          key={group.sourcePaneId}
          data-testid="batch-conflict-source"
          className="mt-2 text-[11px] text-status-warning"
        >
          {t('rebuild.batch_conflict_source', {
            name: group.record.sessionName,
            pane: group.sourcePaneId,
            cwd: group.record.cwd ?? '—',
          })}
        </p>
      ))}

      {excluded.length > 0 && (
        <div className="mt-4">
          <h4 className="text-xs text-text-primary">{t('rebuild.batch_needs_attention')}</h4>
          <p className="text-[11px] text-text-muted">{t('rebuild.batch_needs_attention_hint')}</p>
          <ul className="mt-1 flex flex-col gap-1">
            {excluded.map((pane) => (
              <li
                key={pane.paneId}
                data-testid={`record-attention-${pane.paneId}`}
                className="flex items-center justify-between gap-2 text-xs"
              >
                <span className="truncate font-mono text-text-secondary">
                  {pane.record.sessionName} · {pane.hostId}
                </span>
                <button
                  type="button"
                  data-testid={`record-attention-rebuild-${pane.paneId}`}
                  onClick={() => onRebuildOne(pane)}
                  disabled={busy || lockedOut(`rebuild:${pane.paneId}`)}
                  className="shrink-0 rounded-md border border-border-default px-2.5 py-1 text-xs text-text-secondary hover:text-text-primary hover:border-border-active disabled:opacity-50 disabled:cursor-not-allowed"
                >
                  {t('rebuild.button')}
                </button>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}

function TmuxBlock({
  snap,
  liveByHost,
  busy,
  onRebuild,
  onCommitCwd,
  t,
}: {
  snap: WorkspaceSnapshot
  liveByHost: Record<string, HostLive>
  busy: boolean
  onRebuild: () => void
  onCommitCwd: (hostId: string, code: string, value: string) => void
  t: ReturnType<typeof useI18nStore.getState>['t']
}) {
  const rows: SessionMeta[] = []
  for (const perHost of Object.values(snap.sessionMeta)) {
    for (const meta of Object.values(perHost)) rows.push(meta)
  }

  return (
    <div data-testid="snapshot-tmux-block" className="mt-6">
      <div className="flex items-center justify-between mb-2">
        <h3 className="text-sm text-text-primary">{t('settings.snapshot.tmux.title')}</h3>
        <button
          type="button"
          data-testid="snapshot-rebuild-btn"
          onClick={onRebuild}
          disabled={busy}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-border-default text-text-secondary text-xs hover:text-text-primary hover:border-border-active disabled:opacity-50 disabled:cursor-not-allowed"
        >
          <ArrowsClockwise size={14} />
          {t('settings.snapshot.tmux.rebuildAll')}
        </button>
      </div>

      {rows.length === 0 ? (
        <p className="text-xs text-text-muted">{t('settings.snapshot.tmux.none')}</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-xs text-text-secondary">
            <thead>
              <tr className="text-text-muted text-left">
                <th className="py-1 pr-3 font-normal">{t('settings.snapshot.col.host')}</th>
                <th className="py-1 pr-3 font-normal">{t('settings.snapshot.col.name')}</th>
                <th className="py-1 pr-3 font-normal">{t('settings.snapshot.col.cwd')}</th>
                <th className="py-1 pr-3 font-normal">{t('settings.snapshot.col.command')}</th>
                <th className="py-1 font-normal">{t('settings.snapshot.col.health')}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((meta) => {
                const health = computeHealth(meta, liveByHost[meta.hostId] ?? 'loading')
                return (
                  <tr key={`${meta.hostId}:${meta.sessionCode}`} className="border-t border-border-default">
                    <td className="py-1 pr-3 text-text-primary">{meta.hostId}</td>
                    <td className="py-1 pr-3">{meta.name}</td>
                    <td className="py-1 pr-3 font-mono">
                      <EditableCwdCell
                        cwd={meta.cwd}
                        onCommit={(v) => onCommitCwd(meta.hostId, meta.sessionCode, v)}
                        disabled={busy}
                      />
                    </td>
                    <td className="py-1 pr-3 font-mono">{meta.currentCommand ?? '—'}</td>
                    <td className="py-1">
                      <HealthBadge
                        health={health}
                        testId={`snapshot-health-${meta.hostId}-${meta.sessionCode}`}
                        t={t}
                      />
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

function TabsBlock({
  snap,
  busy,
  onRestoreLayout,
  t,
}: {
  snap: WorkspaceSnapshot
  busy: boolean
  onRestoreLayout: () => void
  t: ReturnType<typeof useI18nStore.getState>['t']
}) {
  return (
    <div data-testid="snapshot-tabs-block" className="mt-6">
      <div className="flex items-center justify-between mb-2">
        <h3 className="text-sm text-text-primary">{t('settings.snapshot.tabs.title')}</h3>
        <button
          type="button"
          data-testid="snapshot-restore-tab-btn"
          onClick={onRestoreLayout}
          disabled={busy}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-border-default text-text-secondary text-xs hover:text-text-primary hover:border-border-active disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {t('settings.snapshot.tabs.restoreLayout')}
        </button>
      </div>

      <ul className="text-xs text-text-secondary flex flex-col gap-2">
        {snap.workspaces.map((ws) => (
          <li key={ws.id} data-testid={`snapshot-ws-${ws.id}`}>
            <span className="text-text-primary">{ws.name}</span>
            <ul className="ml-4 mt-1 flex flex-col gap-1">
              {ws.tabs.map((tabId) => {
                const tab = snap.tabs[tabId]
                if (!tab) return null
                const leaves = collectLeaves(tab.layout)
                return (
                  <li key={tabId} data-testid={`snapshot-tab-${tabId}`}>
                    <ul className="flex flex-col gap-0.5">
                      {leaves.map((pane) => (
                        <li key={pane.id} className="font-mono text-text-muted" data-testid="snapshot-pane-leaf">
                          {leafLabel(pane.content)}
                        </li>
                      ))}
                    </ul>
                  </li>
                )
              })}
            </ul>
          </li>
        ))}
      </ul>
    </div>
  )
}
