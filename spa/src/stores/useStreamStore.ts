// spa/src/stores/useStreamStore.ts
import { create } from 'zustand'
import { subscribeWithSelector } from 'zustand/middleware'
import type { StreamMessage, ControlRequest, StreamConnection } from '../lib/stream-ws'
import type { SessionStatus } from '../components/SessionStatusBadge'

export type HandoffState = 'idle' | 'handoff-in-progress' | 'connected' | 'disconnected'

export interface PerSessionState {
  messages: StreamMessage[]
  pendingControlRequests: ControlRequest[]
  isStreaming: boolean
  conn: StreamConnection | null
  sessionInfo: { ccSessionId: string; model: string }
  cost: number
}

function defaultPerSession(): PerSessionState {
  return {
    messages: [],
    pendingControlRequests: [],
    isStreaming: false,
    conn: null,
    sessionInfo: { ccSessionId: '', model: '' },
    cost: 0,
  }
}

interface StreamStore {
  // Per-session state
  sessions: Record<string, PerSessionState>

  // Global state (keyed by session name but not part of PerSessionState)
  sessionStatus: Record<string, SessionStatus>
  relayStatus: Record<string, boolean>
  handoffState: Record<string, HandoffState>
  handoffProgress: Record<string, string>

  // Per-session actions
  addMessage: (session: string, msg: StreamMessage) => void
  addControlRequest: (session: string, req: ControlRequest) => void
  resolveControlRequest: (session: string, requestId: string) => void
  setStreaming: (session: string, v: boolean) => void
  setSessionInfo: (session: string, ccSessionId: string, model: string) => void
  addCost: (session: string, usd: number) => void
  setConn: (session: string, conn: StreamConnection | null) => void
  loadHistory: (session: string, messages: StreamMessage[]) => void
  clearSession: (session: string) => void

  // Global-keyed actions
  setHandoffState: (session: string, state: HandoffState) => void
  setHandoffProgress: (session: string, progress: string) => void
  setRelayStatus: (session: string, connected: boolean) => void
  setSessionStatus: (session: string, status: SessionStatus) => void
}

function getOrCreate(sessions: Record<string, PerSessionState>, name: string): PerSessionState {
  return sessions[name] ?? defaultPerSession()
}

export const useStreamStore = create<StreamStore>()(subscribeWithSelector((set) => ({
  sessions: {},
  sessionStatus: {},
  relayStatus: {},
  handoffState: {},
  handoffProgress: {},

  addMessage: (session, msg) => set((s) => ({
    sessions: { ...s.sessions, [session]: { ...getOrCreate(s.sessions, session), messages: [...getOrCreate(s.sessions, session).messages, msg] } },
  })),

  addControlRequest: (session, req) => set((s) => {
    const cur = getOrCreate(s.sessions, session)
    return { sessions: { ...s.sessions, [session]: { ...cur, pendingControlRequests: [...cur.pendingControlRequests, req] } } }
  }),

  resolveControlRequest: (session, requestId) => set((s) => {
    const cur = getOrCreate(s.sessions, session)
    return { sessions: { ...s.sessions, [session]: { ...cur, pendingControlRequests: cur.pendingControlRequests.filter((r) => r.request_id !== requestId) } } }
  }),

  setStreaming: (session, v) => set((s) => ({
    sessions: { ...s.sessions, [session]: { ...getOrCreate(s.sessions, session), isStreaming: v } },
  })),

  setSessionInfo: (session, ccSessionId, model) => set((s) => ({
    sessions: { ...s.sessions, [session]: { ...getOrCreate(s.sessions, session), sessionInfo: { ccSessionId, model } } },
  })),

  addCost: (session, usd) => set((s) => {
    const cur = getOrCreate(s.sessions, session)
    return { sessions: { ...s.sessions, [session]: { ...cur, cost: cur.cost + usd } } }
  }),

  setConn: (session, conn) => set((s) => ({
    sessions: { ...s.sessions, [session]: { ...getOrCreate(s.sessions, session), conn } },
  })),

  loadHistory: (session, messages) => set((s) => ({
    sessions: { ...s.sessions, [session]: { ...getOrCreate(s.sessions, session), messages } },
  })),

  clearSession: (session) => set((s) => {
    const cur = s.sessions[session]
    cur?.conn?.close()
    const { [session]: _, ...rest } = s.sessions
    return { sessions: rest }
  }),

  setHandoffState: (session, state) => set((s) => ({
    handoffState: { ...s.handoffState, [session]: state },
  })),

  setHandoffProgress: (session, progress) => set((s) => ({
    handoffProgress: { ...s.handoffProgress, [session]: progress },
  })),

  setRelayStatus: (session, connected) => set((s) => ({
    relayStatus: { ...s.relayStatus, [session]: connected },
  })),

  setSessionStatus: (session, status) => set((s) => ({
    sessionStatus: { ...s.sessionStatus, [session]: status },
  })),
})))
