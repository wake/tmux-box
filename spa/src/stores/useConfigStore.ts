// spa/src/stores/useConfigStore.ts
import { create } from 'zustand'
import { getConfig, updateConfig, type ConfigData } from '../lib/api'

interface ConfigState {
  config: ConfigData | null
  loading: boolean
  fetch: (base: string) => Promise<void>
  update: (base: string, updates: Partial<ConfigData>) => Promise<void>
}

export const useConfigStore = create<ConfigState>((set) => ({
  config: null,
  loading: false,
  fetch: async (base) => {
    set({ loading: true })
    try {
      const config = await getConfig(base)
      set({ config, loading: false })
    } catch {
      set({ loading: false })
    }
  },
  update: async (base, updates) => {
    const config = await updateConfig(base, updates)
    set({ config })
  },
}))
