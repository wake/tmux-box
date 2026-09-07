import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { generateId } from '../lib/id'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'

/* ─── Interfaces ─── */

export interface HostConfig {
  id: string
  name: string
  ip: string
  port: number
  // `null` is the explicit "token cleared, re-auth required" sentinel written by
  // the sync deserialize path when a host is new or its endpoint (ip/port) changed.
  // Distinct from `undefined` (field simply absent); `null` survives JSON round-trips.
  token?: string | null
  order: number
}

export interface HostRuntime {
  status: 'connected' | 'disconnected' | 'reconnecting' | 'auth-error'
  latency?: number
  /**
   * Terminal attach gate (spec §4.6): true only once the *current* host-events
   * connection has delivered and reconciled a `sessions` payload. Closed on
   * every (re)connect and on every drop, so a pane can never attach to a
   * session code that a tmux restart handed to a stranger. Not persisted.
   */
  attachReady?: boolean
  daemonState?: 'connected' | 'refused' | 'unreachable' | 'auth-error'
  tmuxState?: 'ok' | 'unavailable'
  manualRetry?: () => Promise<void> | void  // safe: runtime excluded from persist partialize
}

export interface HostInfo {
  host_id: string
  tmux_instance: string
  purdex_version: string
  tmux_version: string
  os: string
  arch: string
}

/* ─── Store ─── */

interface HostState {
  hosts: Record<string, HostConfig>
  hostOrder: string[]
  runtime: Record<string, HostRuntime>
  activeHostId: string | null

  addHost: (opts: { id?: string; name: string; ip: string; port: number; token?: string | null }) => string
  updateHost: (hostId: string, updates: Partial<Pick<HostConfig, 'name' | 'ip' | 'port' | 'token'>>) => void
  removeHost: (hostId: string) => void
  reorderHosts: (orderedIds: string[]) => void
  setActiveHost: (hostId: string) => void
  setRuntime: (hostId: string, runtime: Partial<HostRuntime>) => void
  getDaemonBase: (hostId: string) => string
  getWsBase: (hostId: string) => string
  getAuthHeaders: (hostId: string) => Record<string, string>
  reset: () => void
}

const DEFAULT_ID = generateId()

function createDefaultState() {
  const defaultHost: HostConfig = {
    id: DEFAULT_ID,
    name: 'mlab',
    ip: '100.64.0.2',
    port: 7860,
    order: 0,
  }
  return {
    hosts: { [DEFAULT_ID]: defaultHost },
    hostOrder: [DEFAULT_ID],
    runtime: {} as Record<string, HostRuntime>,
    activeHostId: DEFAULT_ID as string | null,
  }
}

export const useHostStore = create<HostState>()(
  persist(
    (set, get) => ({
      ...createDefaultState(),

      addHost: (opts) => {
        const id = opts.id ?? generateId()
        // Dedup: if host already exists, just return existing id
        if (get().hosts[id]) return id
        const order = get().hostOrder.length
         
        const { id: _discardId, ...restOpts } = opts
        const host: HostConfig = { id, ...restOpts, order }
        set((state) => ({
          hosts: { ...state.hosts, [id]: host },
          hostOrder: [...state.hostOrder, id],
        }))
        return id
      },

      updateHost: (hostId, updates) =>
        set((state) => {
          const host = state.hosts[hostId]
          if (!host) return state
          return {
            hosts: { ...state.hosts, [hostId]: { ...host, ...updates } },
          }
        }),

      removeHost: (hostId) =>
        set((state) => {
          if (Object.keys(state.hosts).length <= 1) return state
           
          const { [hostId]: _, ...rest } = state.hosts
          const newOrder = state.hostOrder.filter((id) => id !== hostId)
           
          const { [hostId]: __, ...restRuntime } = state.runtime
          const activeHostId =
            state.activeHostId === hostId ? newOrder[0] ?? null : state.activeHostId
          return {
            hosts: rest,
            hostOrder: newOrder,
            runtime: restRuntime,
            activeHostId,
          }
        }),

      reorderHosts: (orderedIds) =>
        set((state) => {
          const hosts = { ...state.hosts }
          orderedIds.forEach((id, i) => {
            if (hosts[id]) hosts[id] = { ...hosts[id], order: i }
          })
          return { hosts, hostOrder: orderedIds }
        }),

      setActiveHost: (hostId) =>
        set((state) => (state.hosts[hostId] ? { activeHostId: hostId } : state)),

      setRuntime: (hostId, runtime) =>
        set((state) => ({
          runtime: {
            ...state.runtime,
            [hostId]: { ...state.runtime[hostId], ...runtime } as HostRuntime,
          },
        })),

      getDaemonBase: (hostId) => {
        const host = get().hosts[hostId]
        if (host) return `http://${host.ip}:${host.port}`
        const fallbackId = get().activeHostId ?? get().hostOrder[0]
        const fallback = fallbackId ? get().hosts[fallbackId] : undefined
        if (!fallback) return 'http://127.0.0.1:7860'
        return `http://${fallback.ip}:${fallback.port}`
      },

      getWsBase: (hostId) => {
        const host = get().hosts[hostId]
        if (host) return `ws://${host.ip}:${host.port}`
        const fallbackId = get().activeHostId ?? get().hostOrder[0]
        const fallback = fallbackId ? get().hosts[fallbackId] : undefined
        if (!fallback) return 'ws://127.0.0.1:7860'
        return `ws://${fallback.ip}:${fallback.port}`
      },

      getAuthHeaders: (hostId) => {
        const host = get().hosts[hostId]
        if (!host?.token) return {} as Record<string, string>
        return { Authorization: `Bearer ${host.token}` }
      },

      reset: () => set(createDefaultState()),
    }),
    {
      name: STORAGE_KEYS.HOSTS,
      storage: purdexStorage,
      version: 1,
      partialize: (state) => ({
        hosts: state.hosts,
        hostOrder: state.hostOrder,
        activeHostId: state.activeHostId,
      }),
    },
  ),
)

syncManager.register(STORAGE_KEYS.HOSTS, useHostStore)
