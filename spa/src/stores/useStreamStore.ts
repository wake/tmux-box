// spa/src/stores/useStreamStore.ts
import { create } from 'zustand'
import type { StreamMessage, ControlRequest } from '../lib/stream-ws'

interface StreamState {
  messages: StreamMessage[]
  pendingControlRequests: ControlRequest[]
  isStreaming: boolean
  sessionId: string | null
  model: string | null
  cost: number

  addMessage: (msg: StreamMessage) => void
  addControlRequest: (req: ControlRequest) => void
  resolveControlRequest: (requestId: string) => void
  setStreaming: (v: boolean) => void
  setSessionInfo: (sessionId: string, model: string) => void
  addCost: (usd: number) => void
  clear: () => void
}

export const useStreamStore = create<StreamState>((set) => ({
  messages: [],
  pendingControlRequests: [],
  isStreaming: false,
  sessionId: null,
  model: null,
  cost: 0,

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

  clear: () => set({ messages: [], pendingControlRequests: [], isStreaming: false, cost: 0 }),
}))
