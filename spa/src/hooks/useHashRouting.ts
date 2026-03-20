import { useEffect, useState } from 'react'
import { useTabStore } from '../stores/useTabStore'
import { parseHash, setHash } from '../lib/hash-routing'

export function useHashRouting(activeTabId: string | null, setActiveTab: (id: string) => void) {
  const activeTab = useTabStore((s) => activeTabId ? s.tabs[activeTabId] ?? null : null)
  const viewMode = activeTab?.viewMode

  // Wait for persist hydration before restoring from URL
  const [hydrated, setHydrated] = useState(() => useTabStore.persist.hasHydrated())
  useEffect(() => {
    if (hydrated) return
    const unsub = useTabStore.persist.onFinishHydration(() => setHydrated(true))
    return unsub
  }, [hydrated])

  // Restore tab + viewMode from URL after hydration
  useEffect(() => {
    if (!hydrated) return
    const { tabId, viewMode: urlViewMode } = parseHash()
    if (tabId && useTabStore.getState().tabs[tabId]) {
      setActiveTab(tabId)
      if (urlViewMode) {
        useTabStore.getState().setViewMode(tabId, urlViewMode)
      }
    }
  }, [hydrated, setActiveTab])

  // Sync activeTabId + viewMode → URL (only after hydrated to avoid overwriting URL)
  useEffect(() => {
    if (!hydrated) return
    if (activeTabId) setHash(activeTabId, viewMode ?? undefined)
  }, [activeTabId, viewMode, hydrated])

  // Listen for browser back/forward
  useEffect(() => {
    const handler = () => {
      const { tabId, viewMode: urlViewMode } = parseHash()
      if (tabId && useTabStore.getState().tabs[tabId]) {
        setActiveTab(tabId)
        if (urlViewMode) {
          useTabStore.getState().setViewMode(tabId, urlViewMode)
        }
      }
    }
    window.addEventListener('hashchange', handler)
    return () => window.removeEventListener('hashchange', handler)
  }, [setActiveTab])
}
