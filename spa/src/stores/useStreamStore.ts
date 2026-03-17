// spa/src/stores/useStreamStore.ts
import { create } from 'zustand'
import type { StreamMessage, ControlRequest, StreamConnection } from '../lib/stream-ws'

export type HandoffState = 'idle' | 'handoff-in-progress' | 'connected' | 'disconnected'

interface StreamState {
  messages: StreamMessage[]
  pendingControlRequests: ControlRequest[]
  isStreaming: boolean
  sessionId: string | null
  model: string | null
  cost: number
  conn: StreamConnection | null
  handoffState: HandoffState

  addMessage: (msg: StreamMessage) => void
  addControlRequest: (req: ControlRequest) => void
  resolveControlRequest: (requestId: string) => void
  setStreaming: (v: boolean) => void
  setSessionInfo: (sessionId: string, model: string) => void
  addCost: (usd: number) => void
  setConn: (conn: StreamConnection | null) => void
  setHandoffState: (state: HandoffState) => void
  clear: () => void
}

export const useStreamStore = create<StreamState>((set) => ({
  messages: [],
  pendingControlRequests: [],
  isStreaming: false,
  sessionId: null,
  model: null,
  cost: 0,
  conn: null,
  handoffState: 'idle',

  addMessage: (msg) => set((s) => ({ messages: [...s.messages, msg] })),

  addControlRequest: (req) =>
    set((s) => ({ pendingControlRequests: [...s.pendingControlRequests, req] })),

  resolveControlRequest: (requestId) =>
    set((s) => ({
      pendingControlRequests: s.pendingControlRequests.filter(
        (r) => r.request_id !== requestId,
      ),
    })),

  setStreaming: (isStreaming) => set({ isStreaming }),

  setSessionInfo: (sessionId, model) => set({ sessionId, model }),

  addCost: (usd) => set((s) => ({ cost: s.cost + usd })),

  setConn: (conn) => set({ conn }),

  setHandoffState: (handoffState) => set({ handoffState }),

  clear: () => set({
    messages: [],
    pendingControlRequests: [],
    isStreaming: false,
    sessionId: null,
    model: null,
    cost: 0,
    handoffState: 'idle',
  }),
}))
