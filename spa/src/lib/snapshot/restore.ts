import { createSession, listSessions } from '../host-api'
import type { Session } from '../host-api'
import { scanPaneTree, updatePaneInLayout } from '../pane-tree'
import type { PaneContent, PaneLayout, Tab } from '../../types/tab'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useSessionStore } from '../../stores/useSessionStore'
import { withOperationLock, type OperationLockGrant } from '../../stores/useRebuildStore'
import { buildSnapshot } from './capture'
import { readPrevSnapshot, writePrevSnapshot } from './storage'
import { RestoreError } from './types'
import type {
  EnsureReport,
  Remap,
  RemapEntry,
  RestoreReport,
  SessionMeta,
  WorkspaceSnapshot,
} from './types'

/**
 * Reconcile persisted per-host session metadata against each host's live
 * session list, reattaching survivors and (optionally) rebuilding restorable
 * dead sessions.
 *
 * Contract highlights (see plan §8.1):
 * - Exactly one `listSessions` per host. If it throws (host offline), every
 *   entry for that host is `failed` and `createSession` is never called there.
 * - Rebuilt entries ALWAYS trust the returned Session object for `newCode` /
 *   `session` — the daemon may auto-rename or assign a different code.
 * - Per-session failure isolation: a single `createSession` rejection marks
 *   only that entry `failed` and never aborts the rest.
 * - `remap` is nested by hostId then oldCode, so identical code values under
 *   different hosts never collide.
 */
export async function ensureSessions(
  sessionMeta: Record<string, Record<string, SessionMeta>>,
  opts?: { rebuild?: boolean },
): Promise<{ remap: Remap; report: EnsureReport }> {
  const rebuild = opts?.rebuild !== false
  const remap: Remap = {}
  const report: EnsureReport = { reattached: 0, rebuilt: 0, failed: 0 }

  for (const [hostId, perHost] of Object.entries(sessionMeta)) {
    const perHostRemap: Record<string, RemapEntry> = {}
    remap[hostId] = perHostRemap

    let live: Session[] | null
    try {
      live = await listSessions(hostId)
    } catch {
      live = null // host offline — every entry fails, no createSession
    }

    for (const [oldCode, meta] of Object.entries(perHost)) {
      if (live === null) {
        perHostRemap[oldCode] = { status: 'failed' }
        report.failed++
        continue
      }

      // Reattach ONLY when a live session matches BOTH code AND name. After a
      // daemon/tmux restart a code can be reused for a DIFFERENT session, so a
      // code-only match could adopt the wrong session (wrong cwd/process). A
      // code match with a mismatched name means an unrelated session took the
      // code → do NOT adopt it; fall through to the dead path (rebuild/fail).
      const alive = live.find((s) => s.code === oldCode && s.name === meta.name)
      if (alive) {
        perHostRemap[oldCode] = { status: 'reattached', newCode: oldCode, session: alive }
        report.reattached++
        continue
      }

      if (!rebuild || !meta.restorable || !meta.cwd) {
        perHostRemap[oldCode] = { status: 'failed' }
        report.failed++
        continue
      }

      try {
        const created = await createSession(hostId, meta.name, meta.cwd, meta.mode)
        // §8.1: trust the returned object, never the request values.
        perHostRemap[oldCode] = { status: 'rebuilt', newCode: created.code, session: created }
        report.rebuilt++
      } catch {
        perHostRemap[oldCode] = { status: 'failed' }
        report.failed++
      }
    }
  }

  return { remap, report }
}

/**
 * Rewrite every `tmux-session` pane in a layout tree against a {@link Remap}
 * produced by {@link ensureSessions}, returning a NEW layout (input untouched).
 *
 * Per-pane behaviour (keyed by the composite `[hostId][sessionCode]`):
 * - `reattached` / `rebuilt` → adopt `entry.newCode`, refresh `cachedName` from
 *   `entry.session.name`, stamp `entry.session.tmux_instance` (falling back to
 *   the pane's current instance when the payload has none), and clear any
 *   `terminated` marker (the session is now attachable again).
 * - `failed` → keep the pane's code but mark `terminated: 'tmux-restarted'`.
 *   The reason is FIXED for the restore path (codex plan-review): restore never
 *   guesses `'session-closed'` / `'host-removed'`.
 * - no matching entry → pane left exactly as-is.
 *
 * `opts.onlyTerminated` guards the "rebuild all sessions" action (spec §3.5):
 * when true, only panes that ALREADY carry a `terminated` marker are touched;
 * live panes are never rewritten even if their key matches a remap entry.
 */
export function remapLayoutSessions(
  layout: PaneLayout,
  remap: Remap,
  opts?: { onlyTerminated?: boolean },
): PaneLayout {
  const onlyTerminated = opts?.onlyTerminated === true

  // Collect the intended content updates first (scan does not mutate), then fold
  // them into fresh layouts via updatePaneInLayout so the input is never touched.
  const updates: Array<{ paneId: string; content: PaneContent }> = []

  scanPaneTree(layout, (pane) => {
    const content = pane.content
    if (content.kind !== 'tmux-session') return
    if (onlyTerminated && content.terminated === undefined) return

    const entry = remap[content.hostId]?.[content.sessionCode]
    if (!entry) return

    if (entry.status === 'failed') {
      updates.push({
        paneId: pane.id,
        content: { ...content, terminated: 'tmux-restarted' },
      })
      return
    }

    // reattached | rebuilt — adopt new code/name/generation, clear terminated
    // marker. Stamping the generation is mandatory (spec §4.6.1): the session we
    // re-point to may live on a NEW tmux server, and keeping the pane's old
    // instance would make the next reconciliation mark it dead again. When the
    // payload carries no instance (older daemon / probe failure) keep the one we
    // already know — never downgrade it to ''.
    const { terminated: _cleared, ...rest } = content
    void _cleared
    updates.push({
      paneId: pane.id,
      content: {
        ...rest,
        sessionCode: entry.newCode,
        cachedName: entry.session.name,
        tmuxInstance: entry.session.tmux_instance ?? content.tmuxInstance,
      },
    })
  })

  let result = layout
  for (const { paneId, content } of updates) {
    result = updatePaneInLayout(result, paneId, content)
  }
  return result
}

/**
 * Structural navigation-integrity guard for a {@link WorkspaceSnapshot}, run
 * before restore adopts a snapshot. Validates ONLY tab/workspace navigation
 * references — the ids the UI dereferences to render the tab bar and workspace
 * switcher. Returns on the FIRST failing check with a short human-readable
 * reason.
 *
 * Five checks (all must pass for `ok:true`):
 * 1. Every id in each `workspace.tabs` exists as a key in `snap.tabs`.
 * 2. `snap.activeTabId` is `null` or a key in `snap.tabs`.
 * 3. Each `workspace.activeTabId` is `null` or belongs to THAT workspace's `tabs`.
 * 4. `snap.activeWorkspaceId` is `null` or the id of some workspace.
 * 5. Every id in `snap.tabOrder` exists as a key in `snap.tabs`.
 *
 * Deliberately NOT validated (scope decision R3 C): pane-content semantic
 * references such as `settings.scope.workspaceId`. Those are content, not
 * navigation, and a dangling one must never block a restore.
 */
export function validateSnapshotConsistency(
  snap: WorkspaceSnapshot,
): { ok: true } | { ok: false; reason: string } {
  const tabExists = (id: string): boolean =>
    Object.prototype.hasOwnProperty.call(snap.tabs, id)

  // 1. Every workspace tab id must resolve to a real tab.
  for (const ws of snap.workspaces) {
    for (const tabId of ws.tabs) {
      if (!tabExists(tabId)) {
        return { ok: false, reason: `workspace "${ws.id}" references unknown tab "${tabId}"` }
      }
    }
  }

  // 2. activeTabId (nullable) must resolve to a real tab.
  if (snap.activeTabId !== null && !tabExists(snap.activeTabId)) {
    return { ok: false, reason: `activeTabId "${snap.activeTabId}" is not a known tab` }
  }

  // 3. Each workspace.activeTabId (nullable) must belong to that workspace.
  for (const ws of snap.workspaces) {
    if (ws.activeTabId !== null && !ws.tabs.includes(ws.activeTabId)) {
      return {
        ok: false,
        reason: `workspace "${ws.id}" activeTabId "${ws.activeTabId}" is not in its tabs`,
      }
    }
  }

  // 4. activeWorkspaceId (nullable) must name an existing workspace.
  if (
    snap.activeWorkspaceId !== null &&
    !snap.workspaces.some((ws) => ws.id === snap.activeWorkspaceId)
  ) {
    return {
      ok: false,
      reason: `activeWorkspaceId "${snap.activeWorkspaceId}" is not a known workspace`,
    }
  }

  // 5. Every tabOrder id must resolve to a real tab.
  for (const tabId of snap.tabOrder) {
    if (!tabExists(tabId)) {
      return { ok: false, reason: `tabOrder references unknown tab "${tabId}"` }
    }
  }

  return { ok: true }
}

/**
 * Replace ONLY the tab store's navigation state (does NOT touch the workspace
 * store). `visitHistory` is NOT persisted, so it is not part of the snapshot;
 * instead the CURRENT history is filtered to the ids that still exist in the
 * new `tabOrder` (a subset of the new tab set), so closing the active tab
 * afterwards still resolves to a live tab rather than a ghost.
 */
export function replaceTabState(
  tabs: Record<string, Tab>,
  tabOrder: string[],
  activeTabId: string | null,
): void {
  const order = new Set(tabOrder)
  const visitHistory = useTabStore.getState().visitHistory.filter((id) => order.has(id))
  useTabStore.setState({ tabs, tabOrder, activeTabId, visitHistory })
}

/**
 * Adopt a {@link WorkspaceSnapshot} into BOTH the tab store and the workspace
 * store with best-effort atomicity.
 *
 * Sequence:
 * 1. `validateSnapshotConsistency` — on failure THROW before touching any
 *    store (navigation refs must be sound before the UI dereferences them).
 * 2. Snapshot the OLD values of both stores for rollback.
 * 3. Replace the tab store, THEN the workspace store.
 * 4. If ANY setState throws, roll BOTH stores back to the captured old values
 *    and rethrow — never leave a half-applied state. If the FIRST (tab)
 *    setState throws, the workspace store was never given new values, so its
 *    rollback is a no-op restoring the untouched old world.
 *
 * `visitHistory` is filtered against `snap.tabOrder` exactly as in
 * {@link replaceTabState} (it is not carried in the snapshot).
 */
export function replaceTabSnapshot(snap: WorkspaceSnapshot): void {
  const result = validateSnapshotConsistency(snap)
  if (!result.ok) {
    throw new Error(`snapshot consistency check failed: ${result.reason}`)
  }

  const tabState = useTabStore.getState()
  const oldTab = {
    tabs: tabState.tabs,
    tabOrder: tabState.tabOrder,
    activeTabId: tabState.activeTabId,
    visitHistory: tabState.visitHistory,
  }
  const wsState = useWorkspaceStore.getState()
  const oldWs = {
    workspaces: wsState.workspaces,
    activeWorkspaceId: wsState.activeWorkspaceId,
  }

  const order = new Set(snap.tabOrder)
  const visitHistory = tabState.visitHistory.filter((id) => order.has(id))

  try {
    useTabStore.setState({
      tabs: snap.tabs,
      tabOrder: snap.tabOrder,
      activeTabId: snap.activeTabId,
      visitHistory,
    })
    useWorkspaceStore.setState({
      workspaces: snap.workspaces,
      activeWorkspaceId: snap.activeWorkspaceId,
    })
  } catch (err) {
    // Roll BOTH stores back. If the tab setState threw, this restores it; the
    // workspace rollback restores the (untouched) old world either way.
    useTabStore.setState(oldTab)
    useWorkspaceStore.setState(oldWs)
    throw err
  }
}

// ---------------------------------------------------------------------------
// T7 — restore orchestration
// ---------------------------------------------------------------------------

/**
 * Determinism seam for the orchestrators that snapshot the pre-restore world
 * into the `-prev` backup. `now` is injected so tests never touch the clock;
 * `buildSnapshotFn` lets tests substitute the capture path entirely. The UI
 * layer (a later task) passes a real `Date.now()`; everything defaults here so
 * production callers can invoke the orchestrators with no second argument.
 */
export interface RestoreDeps {
  now?: number
  buildSnapshotFn?: typeof buildSnapshot
  /**
   * Internal: the operation-lock grant the CALLER already holds. Only
   * {@link undoLastRestore} sets it, so the {@link restoreAll} it delegates to
   * re-enters the undo's own lock instead of being refused by it. The grant
   * itself, not its owner name: names are not identities, and re-entry granted
   * on a name match would let two unrelated callers interleave.
   */
  lockGrant?: OperationLockGrant
}

/**
 * Operation-lock owners for the legacy snapshot actions (spec §4.11). All four
 * create tmux sessions and/or replace the whole tab tree, so none of them may
 * interleave with a Tab Rebuild — see {@link useRebuildStore}.
 */
export const SNAPSHOT_LOCK_OWNER = {
  rebuildAll: 'snapshot:rebuildAllSessions',
  restoreLayout: 'snapshot:restoreTabLayout',
  restoreAll: 'snapshot:restoreAll',
  undo: 'snapshot:undoLastRestore',
} as const

/**
 * Refuse rather than run: a legacy restore replaces the entire tab snapshot,
 * which would silently overwrite an in-flight rebuild's re-point.
 */
function lockRefused(owner: string, holder: string): never {
  throw new Error(`${owner} refused: another operation is already running (${holder})`)
}

/**
 * Collect the daemon sessions that were freshly rebuilt (their tmux processes
 * now exist) but which the front-end could NOT attach to a pane — reported to
 * the user via {@link RestoreError} so nothing is silently orphaned. hostId
 * comes from the outer remap key; name/cwd from the created Session object.
 */
function collectRebuilt(remap: Remap): RestoreReport['rebuiltButUnattached'] {
  const out: RestoreReport['rebuiltButUnattached'] = []
  for (const [hostId, perHost] of Object.entries(remap)) {
    for (const entry of Object.values(perHost)) {
      if (entry.status === 'rebuilt') {
        out.push({ hostId, name: entry.session.name, cwd: entry.session.cwd })
      }
    }
  }
  return out
}

/**
 * Push the reconciled sessions from a {@link Remap} into the session store.
 *
 * CRITICAL (codex plan-review): `useSessionStore.replaceHost` replaces the
 * ENTIRE session array for a host. Calling it per-entry would clobber every
 * earlier session under that host. So we aggregate ALL `reattached`/`rebuilt`
 * sessions for a host into ONE array and call `replaceHost` exactly once per
 * host. `failed` entries contribute no Session object.
 */
export function syncSessionStore(remap: Remap): void {
  const state = useSessionStore.getState()
  const { replaceHost } = state
  for (const [hostId, perHost] of Object.entries(remap)) {
    // `replaceHost` overwrites the host's whole session array, so seed from the
    // host's EXISTING sessions and upsert by code — otherwise any live session
    // on this host that isn't part of this snapshot (e.g. opened outside it)
    // would be wiped from the store and vanish from the session-backed UI.
    const byCode = new Map<string, Session>(
      (state.sessions[hostId] ?? []).map((s) => [s.code, s]),
    )
    // Drop this snapshot's own old codes first: a rebuilt oldCode→newCode (or a
    // `failed` entry) must not leave the dead oldCode lingering as a ghost
    // session. Unrelated live sessions on this host (not in the snapshot) are
    // untouched, so they survive; reattached entries re-add their own code below.
    for (const oldCode of Object.keys(perHost)) byCode.delete(oldCode)
    for (const entry of Object.values(perHost)) {
      if (entry.status === 'reattached' || entry.status === 'rebuilt') {
        byCode.set(entry.session.code, entry.session)
      }
    }
    replaceHost(hostId, Array.from(byCode.values()))
  }
}

/** Rewrite every tab's layout in a snapshot against a remap (input untouched). */
function rewriteSnapshotTabs(
  snap: WorkspaceSnapshot,
  remap: Remap,
  opts: { onlyTerminated?: boolean },
): WorkspaceSnapshot {
  const tabs: Record<string, Tab> = {}
  for (const [id, t] of Object.entries(snap.tabs)) {
    tabs[id] = { ...t, layout: remapLayoutSessions(t.layout, remap, opts) }
  }
  return { ...snap, tabs }
}

/**
 * "Rebuild all sessions" action (spec §3.5, product decision "a"):
 * non-destructive rebuild of every restorable-and-dead session in the snapshot
 * — including orphans not referenced by any current tab — WITHOUT replacing the
 * live tab/workspace structure. Only panes ALREADY terminated adopt the new
 * codes (`onlyTerminated`), so live panes are never disturbed.
 *
 * Does NOT write the `-prev` backup (there is nothing to undo — the existing
 * structure is preserved) and cannot reject on a validation failure because it
 * uses {@link replaceTabState}, which does not validate navigation refs.
 */
export async function rebuildAllSessions(snap: WorkspaceSnapshot): Promise<RestoreReport> {
  return withOperationLock(
    SNAPSHOT_LOCK_OWNER.rebuildAll,
    () => runRebuildAllSessions(snap),
    (holder) => lockRefused(SNAPSHOT_LOCK_OWNER.rebuildAll, holder),
  )
}

async function runRebuildAllSessions(snap: WorkspaceSnapshot): Promise<RestoreReport> {
  const { remap, report } = await ensureSessions(snap.sessionMeta)

  const { tabs, tabOrder, activeTabId } = useTabStore.getState()
  const rewritten: Record<string, Tab> = {}
  for (const [id, t] of Object.entries(tabs)) {
    rewritten[id] = { ...t, layout: remapLayoutSessions(t.layout, remap, { onlyTerminated: true }) }
  }

  replaceTabState(rewritten, tabOrder, activeTabId)
  syncSessionStore(remap)
  return { ...report, rebuiltButUnattached: [] }
}

/**
 * "Restore tab layout" action: adopt the SNAPSHOT's tab/workspace structure but
 * only reattach still-live sessions — dead sessions are marked terminated
 * (`rebuild:false`, no `createSession`). The pre-restore world is backed up to
 * `-prev` (built with {@link buildSnapshot} so the PRIMARY snapshot key is left
 * intact — plan §T2/B1) BEFORE any store mutation, enabling {@link undoLastRestore}.
 */
export async function restoreTabLayout(
  snap: WorkspaceSnapshot,
  deps?: RestoreDeps,
): Promise<RestoreReport> {
  const owner = SNAPSHOT_LOCK_OWNER.restoreLayout
  return withOperationLock(
    owner,
    () => applySnapshotRestore(snap, { rebuild: false }, deps),
    (holder) => lockRefused(owner, holder),
    deps?.lockGrant,
  )
}

/**
 * "Restore everything" action: full rebuild of restorable-dead sessions PLUS
 * adopting the snapshot's tab/workspace structure. Backs up the pre-restore
 * world to `-prev` (via {@link buildSnapshot}, B1) before mutating stores.
 */
export async function restoreAll(
  snap: WorkspaceSnapshot,
  deps?: RestoreDeps,
): Promise<RestoreReport> {
  const owner = SNAPSHOT_LOCK_OWNER.restoreAll
  return withOperationLock(
    owner,
    () => applySnapshotRestore(snap, { rebuild: true }, deps),
    (holder) => lockRefused(owner, holder),
    deps?.lockGrant,
  )
}

/**
 * Shared body of {@link restoreTabLayout} / {@link restoreAll}: reconcile
 * sessions, back up `-prev`, then atomically replace the tab+workspace stores.
 * If {@link replaceTabSnapshot} throws (bad navigation refs / setState failure),
 * the daemon sessions we rebuilt stay alive — they are surfaced in
 * `RestoreError.report.rebuiltButUnattached` (R3 B) and the front-end stores are
 * rolled back inside {@link replaceTabSnapshot} (T6). We never delete daemon
 * sessions on this path.
 */
async function applySnapshotRestore(
  snap: WorkspaceSnapshot,
  ensureOpts: { rebuild: boolean },
  deps?: RestoreDeps,
): Promise<RestoreReport> {
  const now = deps?.now ?? Date.now()
  const build = deps?.buildSnapshotFn ?? buildSnapshot

  const { remap, report } = await ensureSessions(snap.sessionMeta, ensureOpts)
  const rewritten = rewriteSnapshotTabs(snap, remap, {})

  try {
    // B1: back up the CURRENT world before mutating stores. buildSnapshot MUST be
    // used (never captureSnapshot) so the primary restore-source key is untouched.
    // Kept inside the try so a backup-write failure (e.g. localStorage quota)
    // still discloses the daemon sessions ensureSessions already created.
    writePrevSnapshot(await build(now))
    replaceTabSnapshot(rewritten)
  } catch (cause) {
    throw new RestoreError({ ...report, rebuiltButUnattached: collectRebuilt(remap) }, cause)
  }

  syncSessionStore(remap)
  return { ...report, rebuiltButUnattached: [] }
}

/**
 * Undo the most recent restore by replaying the `-prev` backup through
 * {@link restoreAll}. Returns `null` when there is no backup to undo.
 *
 * The nested {@link restoreAll} is handed THIS action's lock grant, so its
 * acquire is a re-entry that cannot drop the lock when it returns — the undo
 * owns the lock from its first line to its last.
 */
export async function undoLastRestore(deps?: RestoreDeps): Promise<RestoreReport | null> {
  const prev = readPrevSnapshot()
  if (!prev) return null
  const owner = SNAPSHOT_LOCK_OWNER.undo
  return withOperationLock(
    owner,
    (grant) => restoreAll(prev, { ...deps, lockGrant: grant }),
    (holder) => lockRefused(owner, holder),
    deps?.lockGrant,
  )
}
