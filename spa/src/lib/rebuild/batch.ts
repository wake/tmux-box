// spa/src/lib/rebuild/batch.ts — "Rebuild all" over the per-tab records (spec §4.11).
//
// Three rules make this more than a loop over `rebuildPane`:
//
//  1. **The grouping key is `(hostId, tmuxInstance, sessionCode)`.** The
//     instance stays in the key because the same code under two different
//     non-empty instances is genuinely two different historical sessions.
//     Two panes on ONE dead session must produce one create and one resume,
//     not `name` plus `name-2` with the agent resumed twice.
//  2. **Unknown generation (`tmuxInstance === ''`) is excluded**, not merged
//     by code alone — that is exactly the merge-two-different-sessions
//     mistake the key exists to prevent. Those panes are surfaced as "needs
//     attention" and rebuilt one at a time.
//  3. **The lock is taken once**, as `rebuild:batch`, and its GRANT is passed
//     down to every group's engine call (`RebuildDeps.lockGrant`). Re-entry is
//     granted on the strength of that grant alone, never on the owner name —
//     two independent batches share the name and must not interleave.
import { collectLeaves } from '../pane-tree'
import { useTabStore } from '../../stores/useTabStore'
import { useRebuildStore, type OperationLockGrant, type RebuildBinding } from '../../stores/useRebuildStore'
import { rebuildPane, repointMember, type RebuildDeps, type RebuildPlan, type RebuildReport } from './engine'
import { pinHost } from './transport'
import type { PaneRebuildRecord, Tab, TerminatedReason } from '../../types/tab'

/** The batch's lock owner. One name for the whole run, per rule 3 above. */
export const BATCH_LOCK_OWNER = 'rebuild:batch'

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

/** A row the batch can actually act on: dead, on a live host, with a record. */
export interface BatchCandidate extends PaneRef {
  record: PaneRebuildRecord
}

export interface BatchGroup extends PaneRef {
  /** Every pane re-pointed to this group's result, in collection order. */
  paneIds: string[]
  /** Whose record won the conflict resolution — the pane the engine runs on. */
  sourcePaneId: string
  record: PaneRebuildRecord
  plan: RebuildPlan
}

export interface BatchMemberResult {
  paneId: string
  tabId: string
  repointed: boolean
  /** Why not, when `repointed` is false. */
  reason?: string
}

export interface BatchGroupResult {
  sourcePaneId: string
  hostId: string
  sessionCode: string
  tmuxInstance: string
  report: RebuildReport
  /** The group's panes other than the source, and what happened to each. */
  members: BatchMemberResult[]
}

export interface BatchReport {
  status: 'ok' | 'blocked'
  /** Set only when `status` is `'blocked'`: who was holding the lock. */
  blockedBy?: string
  groups: BatchGroupResult[]
  excluded: BatchCandidate[]
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

/**
 * The rows "Rebuild all" may act on: a dead pane that still carries a record.
 *
 * `host-removed` is not one of them — the host it belonged to is gone, so
 * there is nothing to create the session on (`pinHost` would refuse), and the
 * action set says so on the pane itself.
 */
export function batchCandidates(rows: RecordRow[]): BatchCandidate[] {
  const candidates: BatchCandidate[] = []
  for (const row of rows) {
    if (!row.terminated || row.terminated === 'host-removed' || !row.record) continue
    const { cachedName: _cachedName, terminated: _terminated, record, ...ref } = row
    candidates.push({ ...ref, record })
  }
  return candidates
}

/** The plan a batched group runs with. Unverified exact resumes are skipped (§9.1). */
export function planForRecord(record: PaneRebuildRecord): RebuildPlan {
  return {
    createSession: true,
    applyCwd: !!record.cwd,
    runResume: !!record.resumeCommand && !record.unverified,
  }
}

function groupKey(ref: PaneRef): string {
  // NUL cannot occur in a host id, an instance stamp or a session code, so
  // no pair of distinct triples can collide on the joined key.
  return [ref.hostId, ref.tmuxInstance, ref.sessionCode].join('\u0000')
}

/** The fields a user can hand-edit — what "conflicting records" means. */
export function recordsDisagree(a: PaneRebuildRecord, b: PaneRebuildRecord): boolean {
  return a.sessionName !== b.sessionName
    || (a.cwd ?? '') !== (b.cwd ?? '')
    || (a.resumeCommand ?? '') !== (b.resumeCommand ?? '')
}

/**
 * Group candidates into one create-and-resume each.
 *
 * Conflicting hand-edits inside a group resolve to the latest `capturedAt`;
 * ties keep the first pane seen, so the result is stable across renders.
 */
export function groupForBatch(panes: BatchCandidate[]): { groups: BatchGroup[]; excluded: BatchCandidate[] } {
  const byKey = new Map<string, BatchGroup>()
  const excluded: BatchCandidate[] = []

  for (const pane of panes) {
    if (!pane.tmuxInstance) {
      excluded.push(pane)
      continue
    }
    const key = groupKey(pane)
    const group = byKey.get(key)
    if (!group) {
      byKey.set(key, {
        paneId: pane.paneId,
        tabId: pane.tabId,
        hostId: pane.hostId,
        sessionCode: pane.sessionCode,
        tmuxInstance: pane.tmuxInstance,
        paneIds: [pane.paneId],
        sourcePaneId: pane.paneId,
        record: pane.record,
        plan: planForRecord(pane.record),
      })
      continue
    }
    group.paneIds.push(pane.paneId)
    if (pane.record.capturedAt > group.record.capturedAt) {
      group.paneId = pane.paneId
      group.tabId = pane.tabId
      group.sourcePaneId = pane.paneId
      group.record = pane.record
      group.plan = planForRecord(pane.record)
    }
  }

  return { groups: Array.from(byKey.values()), excluded }
}

/** The groups and the leftovers, straight from the live tab tree. */
export function planBatch(tabs: Record<string, Tab>): { groups: BatchGroup[]; excluded: BatchCandidate[] } {
  return groupForBatch(batchCandidates(collectRecordRows(tabs)))
}

/**
 * Recreate every rebuildable dead session in the workspace: one create and one
 * resume per group, with every pane in the group re-pointed onto the result.
 *
 * Never throws — a refusal or a per-group failure lands in the report.
 */
export async function runBatchRebuild(deps: RebuildDeps = {}): Promise<BatchReport> {
  const candidates = batchCandidates(collectRecordRows(useTabStore.getState().tabs))
  const { groups, excluded } = groupForBatch(candidates)
  const tabOfPane = new Map(candidates.map((c) => [c.paneId, c.tabId]))

  const grant = useRebuildStore.getState().acquireOperationLock(BATCH_LOCK_OWNER)
  if (!grant) {
    return { status: 'blocked', blockedBy: useRebuildStore.getState().lockedBy ?? '', groups: [], excluded }
  }
  try {
    const results: BatchGroupResult[] = []
    for (const group of groups) {
      results.push(await runGroup(group, tabOfPane, grant, deps))
    }
    return { status: 'ok', groups: results, excluded }
  } finally {
    useRebuildStore.getState().releaseOperationLock(grant)
  }
}

async function runGroup(
  group: BatchGroup,
  tabOfPane: Map<string, string>,
  grant: OperationLockGrant,
  deps: RebuildDeps,
): Promise<BatchGroupResult> {
  // Every group was planned before the first one ran, so by the time this one
  // starts the user may have re-pointed its source pane. The binding travels
  // into the engine, which verifies it in the same synchronous step as the
  // create — otherwise the group would rebuild whatever the pane holds now.
  const binding: RebuildBinding = {
    hostId: group.hostId,
    sessionCode: group.sessionCode,
    tmuxInstance: group.tmuxInstance,
  }
  const report = await rebuildPane(group.hostId, group.tabId, group.sourcePaneId, group.plan, {
    ...deps,
    lockGrant: grant,
    expectedBinding: binding,
  })
  // The full `Session` the engine created — `report.created` is only its
  // summary, and `defaultRepoint` needs the session itself.
  const created = useRebuildStore.getState().operations[group.sourcePaneId]?.createdSession

  // The host the group pinned. `pinHost` throws when the address, the token or
  // the host itself changed since the create, which is exactly the condition
  // that makes the created code meaningless for these panes.
  const pinnedHost = useRebuildStore.getState().operations[group.sourcePaneId]?.host
  const assertHostUnchanged = () => { pinHost(group.hostId, pinnedHost) }

  const members: BatchMemberResult[] = []
  for (const paneId of group.paneIds) {
    if (paneId === group.sourcePaneId) continue
    const tabId = tabOfPane.get(paneId) ?? ''
    if (!created) {
      members.push({ paneId, tabId, repointed: false, reason: 'nothing was created' })
      continue
    }
    if (report.steps.resume.status === 'failed') {
      // The source pane stays mounted to report the failure and offer the
      // retry; re-pointing its siblings onto a session whose agent never came
      // back would hide that.
      members.push({ paneId, tabId, repointed: false, reason: 'resume failed' })
      continue
    }
    const outcome = repointMember(tabId, paneId, binding, created, deps.repoint, assertHostUnchanged)
    members.push({ paneId, tabId, ...outcome })
  }

  return {
    sourcePaneId: group.sourcePaneId,
    hostId: group.hostId,
    sessionCode: group.sessionCode,
    tmuxInstance: group.tmuxInstance,
    report,
    members,
  }
}
