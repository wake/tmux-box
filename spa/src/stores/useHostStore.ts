import { create } from 'zustand'
import { persist } from 'zustand/middleware'

export interface Host {
  id: string
  name: string
  address: string
  port: number
  status: 'connected' | 'disconnected' | 'connecting'
}

interface HostState {
  hosts: Record<string, Host>
  defaultHost: Host

  getDaemonBase: (hostId: string) => string
  getWsBase: (hostId: string) => string
  updateHost: (hostId: string, updates: Partial<Pick<Host, 'address' | 'port' | 'name'>>) => void
  reset: () => void
}

const DEFAULT_HOST: Host = {
  id: 'local',
  name: 'mlab',
  address: '100.64.0.2',
  port: 7860,
  status: 'connected',
}

function createDefaultState() {
  return { hosts: { [DEFAULT_HOST.id]: DEFAULT_HOST }, defaultHost: DEFAULT_HOST }
}

export const useHostStore = create<HostState>()(
  persist(
    (set, get) => ({
      ...createDefaultState(),

      getDaemonBase: (hostId) => {
        const host = get().hosts[hostId] ?? get().defaultHost
        return `http://${host.address}:${host.port}`
      },

      getWsBase: (hostId) => {
        const host = get().hosts[hostId] ?? get().defaultHost
        return `ws://${host.address}:${host.port}`
      },

      updateHost: (hostId, updates) =>
        set((state) => {
          const host = state.hosts[hostId]
          if (!host) return state
          const updated = { ...host, ...updates }
          return {
            hosts: { ...state.hosts, [hostId]: updated },
            defaultHost: hostId === state.defaultHost.id ? updated : state.defaultHost,
          }
        }),

      reset: () => set(createDefaultState()),
    }),
    {
      name: 'tbox-hosts',
      partialize: (state) => ({ hosts: state.hosts }),
    },
  ),
)
