// spa/src/stores/useSessionStore.ts
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { type Session, listSessions } from '../lib/api'

interface SessionState {
  sessions: Session[]
  activeId: string | null
  fetch: (base: string) => Promise<void>
  setActive: (code: string | null) => void
}

export const useSessionStore = create<SessionState>()(
  persist(
    (set) => ({
      sessions: [],
      activeId: null,
      fetch: async (base: string) => {
        const sessions = await listSessions(base)
        set({ sessions })
      },
      setActive: (code) => set({ activeId: code }),
    }),
    {
      name: 'tbox-sessions',
      partialize: (state) => ({ activeId: state.activeId }),
    },
  ),
)
