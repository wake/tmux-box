import { useEffect } from 'react'
import { useTabStore } from '../stores/useTabStore'
import { parseHash, setHash } from '../lib/hash-routing'

export function useHashRouting(activeTabId: string | null, setActiveTab: (id: string) => void) {
  // Restore tab from URL on mount
  useEffect(() => {
    const { tabId } = parseHash()
    if (tabId && useTabStore.getState().tabs[tabId]) {
      setActiveTab(tabId)
    }
  }, [setActiveTab])

  // Sync activeTabId → URL
  useEffect(() => {
    if (activeTabId) setHash(activeTabId)
  }, [activeTabId])

  // Listen for browser back/forward
  useEffect(() => {
    const handler = () => {
      const { tabId } = parseHash()
      if (tabId && useTabStore.getState().tabs[tabId]) setActiveTab(tabId)
    }
    window.addEventListener('hashchange', handler)
    return () => window.removeEventListener('hashchange', handler)
  }, [setActiveTab])
}
