// spa/src/stores/useRebuildStore.ts — per-pane rebuild operation state (spec §4.8).
//
// The report lives here, keyed by paneId, rather than in component state: the
// pane that shows it is unmounted the moment `terminated` clears
// (`SessionPaneContent.tsx:70-72`), and a partially-failed operation has to
// survive that — and any other remount — so "Retry resume" / "Attach anyway"
// still know which session was created.
//
// Not persisted: an in-flight operation must never outlive the page.
import { create } from 'zustand'
import { bindingEquals, type SessionBinding } from '../lib/rebuild/binding'
import type { Session } from '../lib/host-api'
import type { RebuildPlan, RebuildReport } from '../lib/rebuild/engine'
import type { HostIdentity } from '../lib/rebuild/transport'

/**
 * The pane binding an operation started from — the re-point guard's baseline.
 * Compared with `bindingEquals`: an operation that planned from one generation
 * may not act on a pane that has since moved to another (spec §4.5).
 */
export type RebuildBinding = SessionBinding

export interface RebuildOperation {
  paneId: string
  tabId: string
  hostId: string
  plan: RebuildPlan
  binding: RebuildBinding
  /**
   * The host configuration the operation pinned at its start. "Retry resume"
   * re-pins against it instead of re-resolving the host id, so an address the
   * user edited in between refuses the retry rather than sending the resume
   * command to a different machine.
   *
   * Absent only on a refusal — an operation that never pinned a host, and that
   * has no created session to retry against either.
   */
  host?: HostIdentity
  /** The command step 3 sends; kept so "Retry resume" needs no re-derivation. */
  resumeCommand: string
  /** Set once step 1 succeeds. Its presence is what makes a retry possible. */
  createdSession?: Session
  status: 'running' | 'done'
  report: RebuildReport
  startedAt: number
  finishedAt?: number
}

/**
 * A grant from {@link RebuildState.acquireOperationLock}.
 *
 * `id` is the whole model. Owner names are not identities: two independent
 * `runBatchRebuild` calls both name themselves `rebuild:batch`, so admitting a
 * second acquire on a name match let them interleave — and the first to finish
 * then dropped the lock while the second was still awaiting, opening the door
 * to a legacy snapshot restore. So a grant is identified by an unforgeable
 * symbol minted at the moment the lock was taken, and both nesting and release
 * compare THAT.
 *
 * Nesting is asked for, never inferred: a caller that already holds a grant
 * passes it back in, and gets a grant sharing the holder's `id` with
 * `outermost: false`, whose release is a no-op. That is what lets
 * `undoLastRestore` → `restoreAll` nest without the inner call unlocking the
 * world underneath the caller still relying on it.
 */
export interface OperationLockGrant {
  readonly owner: string
  /** Identity of the grant chain that holds the lock. */
  readonly id: symbol
  readonly outermost: boolean
}

interface RebuildState {
  operations: Record<string, RebuildOperation>
  /**
   * The single global operation lock (spec §4.11). Everything that creates
   * tmux sessions or rewrites the tab tree — the rebuild engine and all five
   * legacy snapshot actions — passes through it, so a legacy restore can never
   * replace the tab snapshot underneath an in-flight rebuild's re-point.
   */
  lockedBy: string | null
  /**
   * The grant that actually took the lock. `lockedBy` is its owner name, kept
   * as its own field because the UI subscribes to it; this is the identity.
   */
  lockGrant: OperationLockGrant | null

  beginOperation: (op: Omit<RebuildOperation, 'status' | 'startedAt' | 'finishedAt'>) => void
  patchOperation: (paneId: string, patch: Partial<RebuildOperation>) => void
  finishOperation: (paneId: string, patch: Partial<RebuildOperation>) => void

  /**
   * A grant when the lock was free, or when `parent` is the grant currently
   * holding it (a nested acquire). `null` in every other case — including a
   * second top-level acquire by an owner of the same name.
   */
  acquireOperationLock: (owner: string, parent?: OperationLockGrant | null) => OperationLockGrant | null
  /** No-op unless the grant is the outermost one the current holder was issued. */
  releaseOperationLock: (grant: OperationLockGrant | null) => void
}

export const useRebuildStore = create<RebuildState>()((set, get) => ({
  operations: {},
  lockedBy: null,
  lockGrant: null,

  beginOperation: (op) =>
    set((state) => ({
      operations: {
        ...state.operations,
        [op.paneId]: { ...op, status: 'running', startedAt: Date.now(), finishedAt: undefined },
      },
    })),

  patchOperation: (paneId, patch) =>
    set((state) => {
      const prev = state.operations[paneId]
      if (!prev) return state
      return { operations: { ...state.operations, [paneId]: { ...prev, ...patch } } }
    }),

  finishOperation: (paneId, patch) =>
    set((state) => {
      const prev = state.operations[paneId]
      if (!prev) return state
      return {
        operations: {
          ...state.operations,
          [paneId]: { ...prev, ...patch, status: 'done', finishedAt: Date.now() },
        },
      }
    }),

  acquireOperationLock: (owner, parent) => {
    if (get().lockedBy === null) {
      const grant: OperationLockGrant = { owner, id: Symbol(owner), outermost: true }
      set({ lockedBy: owner, lockGrant: grant })
      return grant
    }
    // Held. The only way in is to already be inside it: the caller hands back
    // the grant it is holding, and gets a re-entry grant on the same chain.
    // A name match is NOT enough — that is what let two batches interleave.
    const held = get().lockGrant
    if (parent && held && parent.id === held.id) {
      return { owner, id: held.id, outermost: false }
    }
    return null
  },

  releaseOperationLock: (grant) => {
    if (!grant || !grant.outermost) return
    // A grant that is not the current holder's (forged, or left over from an
    // earlier operation under the same name) must never unlock somebody
    // else's work.
    set((state) => (state.lockGrant?.id === grant.id ? { lockedBy: null, lockGrant: null } : state))
  },
}))

/**
 * The pane's operation, scoped to the rebuild cycle it started from.
 *
 * An operation is stored per pane — `retryResume` / `attachAnyway` are
 * pane-addressed and the entry has to outlive the panel's remount — but it
 * describes ONE binding: the `(hostId, sessionCode, tmuxInstance)` the pane
 * held when the operation began. Once the pane moves off that binding (a
 * successful re-point, the session picker, a later reconciliation), the
 * operation no longer describes the pane in front of the user, and a pane that
 * dies again must present a clean panel rather than the previous cycle's
 * frozen rows.
 *
 * Scoping on the READ rather than clearing on an event is deliberate:
 * staleness is a property of the data — the binding no longer matches — not of
 * an event some lifecycle hook has to be watching for and can miss.
 */
export function usePaneOperation(paneId: string, binding?: RebuildBinding): RebuildOperation | undefined {
  return useRebuildStore((s) => {
    const op = s.operations[paneId]
    if (!op) return undefined
    return !binding || bindingEquals(op.binding, binding) ? op : undefined
  })
}

/**
 * Run `body` while holding the operation lock for `owner`, releasing it however
 * the body settles. `body` is handed the grant, so a nested call can thread it
 * on as its own `parent` — the only way into a lock somebody already holds.
 * When the lock is held and `parent` is not that holder's grant, `onRefused` is
 * called with the holder's name and the body never runs.
 */
export async function withOperationLock<T>(
  owner: string,
  body: (grant: OperationLockGrant) => Promise<T>,
  onRefused: (holder: string) => T,
  parent?: OperationLockGrant | null,
): Promise<T> {
  const grant = useRebuildStore.getState().acquireOperationLock(owner, parent)
  if (!grant) return onRefused(useRebuildStore.getState().lockedBy ?? '')
  try {
    return await body(grant)
  } finally {
    useRebuildStore.getState().releaseOperationLock(grant)
  }
}
