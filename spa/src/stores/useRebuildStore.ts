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

interface RebuildState {
  operations: Record<string, RebuildOperation>
  /**
   * The single global operation lock. Declared here now so the store shape is
   * settled; the locking policy itself arrives with Task 14.
   */
  lockedBy: string | null

  beginOperation: (op: Omit<RebuildOperation, 'status' | 'startedAt' | 'finishedAt'>) => void
  patchOperation: (paneId: string, patch: Partial<RebuildOperation>) => void
  finishOperation: (paneId: string, patch: Partial<RebuildOperation>) => void
  clearOperation: (paneId: string) => void
}

export const useRebuildStore = create<RebuildState>()((set) => ({
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
}))
