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
import type { Session } from '../lib/host-api'
import type { RebuildPlan, RebuildReport } from '../lib/rebuild/engine'

/** The pane binding an operation started from — the re-point guard's baseline. */
export interface RebuildBinding {
  hostId: string
  sessionCode: string
  tmuxInstance: string
}

export interface RebuildOperation {
  paneId: string
  tabId: string
  hostId: string
  plan: RebuildPlan
  binding: RebuildBinding
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
 * `outermost` is the whole model: a nested acquire by the SAME owner hands
 * back a re-entry token whose release is a no-op, so an inner call
 * (`undoLastRestore` → `restoreAll`) can never unlock the world underneath the
 * caller that is still relying on it. Only the outermost token releases.
 */
export interface OperationLockToken {
  readonly owner: string
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

  beginOperation: (op: Omit<RebuildOperation, 'status' | 'startedAt' | 'finishedAt'>) => void
  patchOperation: (paneId: string, patch: Partial<RebuildOperation>) => void
  finishOperation: (paneId: string, patch: Partial<RebuildOperation>) => void
  clearOperation: (paneId: string) => void

  /** A token when granted, `null` when a DIFFERENT owner already holds it. */
  acquireOperationLock: (owner: string) => OperationLockToken | null
  /** No-op unless the token is the outermost grant of the current holder. */
  releaseOperationLock: (token: OperationLockToken | null) => void
}

export const useRebuildStore = create<RebuildState>()((set, get) => ({
  operations: {},
  lockedBy: null,

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

  clearOperation: (paneId) =>
    set((state) => {
      if (!state.operations[paneId]) return state
      const { [paneId]: _dropped, ...rest } = state.operations
      return { operations: rest }
    }),

  acquireOperationLock: (owner) => {
    const held = get().lockedBy
    if (held === null) {
      set({ lockedBy: owner })
      return { owner, outermost: true }
    }
    // Same owner → re-entry. NOTE for the engine: `rebuild:<paneId>` is
    // pane-specific, so re-entry alone would let two operations on one pane
    // both proceed. The engine refuses that on `operations[paneId].status`
    // BEFORE it ever gets here.
    if (held === owner) return { owner, outermost: false }
    return null
  },

  releaseOperationLock: (token) => {
    if (!token || !token.outermost) return
    // A token that is not the current holder's (forged, or left over from an
    // earlier operation) must never unlock somebody else's work.
    set((state) => (state.lockedBy === token.owner ? { lockedBy: null } : state))
  },
}))

/**
 * Run `body` while holding the operation lock for `owner`, releasing it however
 * the body settles. When another owner holds it, `onRefused` is called with the
 * holder's name and the body never runs.
 */
export async function withOperationLock<T>(
  owner: string,
  body: () => Promise<T>,
  onRefused: (holder: string) => T,
): Promise<T> {
  const token = useRebuildStore.getState().acquireOperationLock(owner)
  if (!token) return onRefused(useRebuildStore.getState().lockedBy ?? '')
  try {
    return await body()
  } finally {
    useRebuildStore.getState().releaseOperationLock(token)
  }
}
