import { useEffect, useRef, useMemo } from 'react'
import { useUISettingsStore } from '../stores/useUISettingsStore'

interface MinimalTab {
  id: string
  pinned: boolean
}

export function useTabAlivePool(activeTabId: string | null, tabs: MinimalTab[]) {
  const keepAliveCount = useUISettingsStore((s) => s.keepAliveCount)
  const keepAlivePinned = useUISettingsStore((s) => s.keepAlivePinned)
  const settingsVersion = useUISettingsStore((s) => s.terminalSettingsVersion)

  // LRU history: most-recent-first
  const historyRef = useRef<string[]>([])
  const prevVersionRef = useRef(settingsVersion)

  // Clear history on settings version bump (synchronous, before useMemo)
  if (settingsVersion !== prevVersionRef.current) {
    historyRef.current = []
    prevVersionRef.current = settingsVersion
  }

  // Update LRU history synchronously during render so useMemo reads fresh data
  if (activeTabId) {
    const h = historyRef.current
    const idx = h.indexOf(activeTabId)
    if (idx !== 0) {
      if (idx > 0) h.splice(idx, 1)
      h.unshift(activeTabId)
    }
  }

  const tabMap = useMemo(() => new Map(tabs.map((t) => [t.id, t])), [tabs])

  const aliveIds = useMemo(() => {
    const h = historyRef.current
    if (keepAliveCount === 0) {
      return activeTabId ? [activeTabId] : []
    }

    const alive: string[] = []
    const pinnedAlive: string[] = []
    let normalCount = 0
    const maxNormal = keepAliveCount + 1 // +1 for active tab

    for (const id of h) {
      const tab = tabMap.get(id)
      if (!tab) continue
      if (keepAlivePinned && tab.pinned) {
        pinnedAlive.push(id)
      } else {
        if (normalCount < maxNormal) {
          alive.push(id)
          normalCount++
        }
      }
    }
    return [...pinnedAlive, ...alive]
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeTabId, keepAliveCount, keepAlivePinned, tabMap, settingsVersion])

  // Trim history to prevent unbounded growth
  useEffect(() => {
    const maxHistory = Math.max(keepAliveCount + 10, 20)
    if (historyRef.current.length > maxHistory) {
      historyRef.current = historyRef.current.slice(0, maxHistory)
    }
  }, [activeTabId, keepAliveCount])

  return { aliveIds, poolVersion: settingsVersion }
}
