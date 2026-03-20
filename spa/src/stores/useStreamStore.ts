// spa/src/stores/useStreamStore.ts
import { create } from 'zustand'
import { subscribeWithSelector } from 'zustand/middleware'
import type { StreamMessage, ControlRequest, StreamConnection } from '../lib/stream-ws'
import type { SessionStatus } from '../components/SessionStatusBadge'

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
  handoffProgress: {},

  addMessage: (session, msg) => set((s) => {
    const cur = getOrCreate(s.sessions, session)
    return { sessions: { ...s.sessions, [session]: { ...cur, messages: [...cur.messages, msg] } } }
  }),

  addControlRequest: (session, req) => set((s) => {
    const cur = getOrCreate(s.sessions, session)
    return { sessions: { ...s.sessions, [session]: { ...cur, pendingControlRequests: [...cur.pendingControlRequests, req] } } }
  }),

  resolveControlRequest: (session, requestId) => set((s) => {
    const cur = getOrCreate(s.sessions, session)
    return { sessions: { ...s.sessions, [session]: { ...cur, pendingControlRequests: cur.pendingControlRequests.filter((r) => r.request_id !== requestId) } } }
  }),

  setStreaming: (session, v) => set((s) => {
    const cur = getOrCreate(s.sessions, session)
    return { sessions: { ...s.sessions, [session]: { ...cur, isStreaming: v } } }
  }),

  setSessionInfo: (session, ccSessionId, model) => set((s) => {
    const cur = getOrCreate(s.sessions, session)
    return { sessions: { ...s.sessions, [session]: { ...cur, sessionInfo: { ccSessionId, model } } } }
  }),

  addCost: (session, usd) => set((s) => {
    const cur = getOrCreate(s.sessions, session)
    return { sessions: { ...s.sessions, [session]: { ...cur, cost: cur.cost + usd } } }
  }),

  setConn: (session, conn) => set((s) => {
    const cur = getOrCreate(s.sessions, session)
    return { sessions: { ...s.sessions, [session]: { ...cur, conn } } }
  }),

  // Note: loadHistory replaces all messages. If live messages arrived via
  // addMessage before history loads, they will be lost. In practice this race
  // is narrow (CC waits for user input after --resume), but be aware.
  loadHistory: (session, messages) => set((s) => {
    const cur = getOrCreate(s.sessions, session)
    return { sessions: { ...s.sessions, [session]: { ...cur, messages } } }
  }),

  clearSession: (session) => {
    // Close conn outside set() to avoid re-entrant mutations
    const cur = useStreamStore.getState().sessions[session]
    cur?.conn?.close()
    set((s) => {
      const { [session]: _cleared, ...rest } = s.sessions // eslint-disable-line @typescript-eslint/no-unused-vars
      return { sessions: rest }
    })
  },

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
