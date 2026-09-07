// spa/src/lib/rebuild/eligibility.ts — one decision about a rebuild record,
// read by both the Snapshot section's table and "Rebuild all" (spec §4.11).
//
// The two used to be decided separately and could disagree: the table compared
// the captured code+name against the host's live list, so a tmux restart that
// reused the code for a session of the same name rendered a dead pane 🟢 live
// while the batch — which reads `terminated` — rebuilt it; and a record with no
// cwd rendered ⚠️ structure-only while the batch still created a session for it.
// A row must not say one thing while the button does another, so the verdict
// lives here and both sides read it.
//
// The authority for "is this pane's session still there" is the pane's own
// `terminated` flag, NOT the section's session-list fetch:
//
//  * `terminated` is the generation-scoped verdict the reconciler already
//    reached from a payload that carried its own generation (§4.5, §4.6) —
//    which is exactly what makes a reused code stop reading as live. The
//    code+name comparison it replaces has no generation in it at all.
//  * the fetched list is a one-shot REST read taken when the section mounted.
//    It is fine for "can I reach this host", but a pane that was created or
//    re-pointed after that read is simply absent from it, and treating absence
//    as death would let the batch create a duplicate session for a live pane.
//
// So the host's liveness is a display-only overlay ({@link recordHealth}): it
// can only mask a row as ⚪/⏳, never turn a 🟢 into a 🔴. What the batch acts
// on is the row-intrinsic verdict, and nothing else.
//
// The legacy captured-snapshot table above keeps its own `computeHealth`: its
// rows are `SessionMeta`, not panes, so they have no `terminated` flag and no
// generation to compare — the live list is all the evidence it has.
import { collectLeaves } from '../pane-tree'
import type { PaneRebuildRecord, Tab, TerminatedReason } from '../../types/tab'

/** Where a pane lives and what session generation it is bound to. */
export interface PaneRef {
  paneId: string
  tabId: string
  hostId: string
  sessionCode: string
  tmuxInstance: string
}

/** One row of the Snapshot section's records table — every terminal tmux pane. */
export interface RecordRow extends PaneRef {
  cachedName: string
  terminated?: TerminatedReason
  record?: PaneRebuildRecord
}

/** A row the batch can actually act on: dead, rebuildable, with a record. */
export interface BatchCandidate extends PaneRef {
  record: PaneRebuildRecord
}

/** The four states the section's health indicator renders, plus loading. */
export type RecordHealth = 'loading' | 'live' | 'dead' | 'structure' | 'offline'

/** What the section knows about a host: still asking, unreachable, reachable. */
export type HostLiveness = 'loading' | 'offline' | 'online'

/** Why a row is not something a rebuild may act on. */
export type IneligibleReason =
  /** Still attached to its session — rebuilding would duplicate it. */
  | 'attached'
  /** The host it belonged to is gone; `pinHost` would refuse anyway. */
  | 'host-removed'
  /** Nothing was ever captured for this pane. */
  | 'no-record'
  /** A record that cannot say which directory to launch in. */
  | 'no-cwd'

export interface RecordEligibility {
  /** The badge this row shows once its host's liveness is folded in. */
  health: Exclude<RecordHealth, 'loading'>
  /** Whether a rebuild — batched or single-pane — may act on this row. */
  rebuildable: boolean
  reason?: IneligibleReason
}

/**
 * The row-intrinsic verdict: everything the table and the batch must agree on.
 *
 * `rebuildable` is exactly `health === 'dead'`, which is the invariant that
 * keeps the badge and the button telling the same story.
 *
 * **A record with no cwd is NOT rebuildable.** The engine would happily create
 * the session — `planForRecord` merely leaves `applyCwd` off — and the agent
 * would then be resumed in the daemon's default directory rather than the one
 * the work lives in. That is precisely the ⚠️ "structure only" the snapshot
 * table means, so it is stated once, here, and both surfaces obey it. The user
 * fills the cwd in on the pane itself (§4.10), and the row becomes 🔴.
 */
export function classifyRecord(row: RecordRow): RecordEligibility {
  // Outranks the fetch: the host is gone whatever a session list says.
  if (row.terminated === 'host-removed') return { health: 'offline', rebuildable: false, reason: 'host-removed' }
  if (!row.terminated) return { health: 'live', rebuildable: false, reason: 'attached' }
  if (!row.record) return { health: 'structure', rebuildable: false, reason: 'no-record' }
  if (!row.record.cwd) return { health: 'structure', rebuildable: false, reason: 'no-cwd' }
  return { health: 'dead', rebuildable: true }
}

/**
 * The badge to render: the row's own verdict, masked by what we know about its
 * host. Display only — see the module comment for why liveness never decides
 * whether a row is rebuildable.
 */
export function recordHealth(row: RecordRow, host: HostLiveness): RecordHealth {
  const { health } = classifyRecord(row)
  if (health === 'offline') return 'offline'
  if (host === 'loading') return 'loading'
  if (host === 'offline') return 'offline'
  return health
}

/**
 * Every terminal `tmux-session` pane in the workspace, live ones included —
 * the table shows the whole picture, the batch acts on part of it.
 * Stream panes are out of scope for the whole feature.
 */
export function collectRecordRows(tabs: Record<string, Tab>): RecordRow[] {
  const rows: RecordRow[] = []
  for (const tab of Object.values(tabs)) {
    for (const pane of collectLeaves(tab.layout)) {
      const content = pane.content
      if (content.kind !== 'tmux-session' || content.mode !== 'terminal') continue
      rows.push({
        paneId: pane.id,
        tabId: tab.id,
        hostId: content.hostId,
        sessionCode: content.sessionCode,
        tmuxInstance: content.tmuxInstance,
        cachedName: content.cachedName,
        terminated: content.terminated,
        record: content.rebuild,
      })
    }
  }
  return rows
}

/** The rows a rebuild may act on — the 🔴 ones, and only those. */
export function batchCandidates(rows: RecordRow[]): BatchCandidate[] {
  const candidates: BatchCandidate[] = []
  for (const row of rows) {
    if (!classifyRecord(row).rebuildable || !row.record) continue
    const { cachedName: _cachedName, terminated: _terminated, record, ...ref } = row
    candidates.push({ ...ref, record })
  }
  return candidates
}
