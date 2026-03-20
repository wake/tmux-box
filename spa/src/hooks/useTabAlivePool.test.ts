import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useTabAlivePool } from './useTabAlivePool'
import { useUISettingsStore } from '../stores/useUISettingsStore'

function resetSettings(overrides?: { keepAliveCount?: number; keepAlivePinned?: boolean }) {
  useUISettingsStore.setState({
    keepAliveCount: overrides?.keepAliveCount ?? 0,
    keepAlivePinned: overrides?.keepAlivePinned ?? false,
    terminalSettingsVersion: 0,
    terminalRevealDelay: 300,
    terminalRenderer: 'webgl',
  })
}

interface MockTab { id: string; pinned: boolean }

describe('useTabAlivePool', () => {
  beforeEach(() => resetSettings())

  it('keepAliveCount=0: pool only contains activeTabId', () => {
    const tabs: MockTab[] = [
      { id: 'a', pinned: false },
      { id: 'b', pinned: false },
    ]
    const { result } = renderHook(() => useTabAlivePool('a', tabs))
    expect(result.current.aliveIds).toEqual(['a'])
  })

  it('keepAliveCount=0: no activeTab returns empty pool', () => {
    const { result } = renderHook(() => useTabAlivePool(null, []))
    expect(result.current.aliveIds).toEqual([])
  })

  it('keepAliveCount=2: pool keeps last 2+1 visited tabs', () => {
    resetSettings({ keepAliveCount: 2 })
    const tabs: MockTab[] = [
      { id: 'a', pinned: false },
      { id: 'b', pinned: false },
      { id: 'c', pinned: false },
      { id: 'd', pinned: false },
    ]

    const { result, rerender } = renderHook(
      ({ activeId }) => useTabAlivePool(activeId, tabs),
      { initialProps: { activeId: 'a' as string } },
    )
    // Visit b, c, d
    rerender({ activeId: 'b' })
    rerender({ activeId: 'c' })
    rerender({ activeId: 'd' })
    // Pool: d (active) + c, b (2 kept) = 3 total. a evicted.
    expect(result.current.aliveIds).toContain('d')
    expect(result.current.aliveIds).toContain('c')
    expect(result.current.aliveIds).toContain('b')
    expect(result.current.aliveIds).not.toContain('a')
  })

  it('keepAlivePinned=true: pinned tabs do not count toward pool limit', () => {
    resetSettings({ keepAliveCount: 1, keepAlivePinned: true })
    const tabs: MockTab[] = [
      { id: 'pinned1', pinned: true },
      { id: 'normal1', pinned: false },
      { id: 'normal2', pinned: false },
    ]

    const { result, rerender } = renderHook(
      ({ activeId }) => useTabAlivePool(activeId, tabs),
      { initialProps: { activeId: 'pinned1' as string } },
    )
    rerender({ activeId: 'normal1' })
    rerender({ activeId: 'normal2' })
    // Pool: normal2 (active) + normal1 (1 kept) + pinned1 (exempt). normal1 kept because limit=1+1=2.
    expect(result.current.aliveIds).toContain('normal2')
    expect(result.current.aliveIds).toContain('pinned1')
    expect(result.current.aliveIds).toContain('normal1')
  })

  it('terminalSettingsVersion bump resets pool', () => {
    resetSettings({ keepAliveCount: 3 })
    const tabs: MockTab[] = [
      { id: 'a', pinned: false },
      { id: 'b', pinned: false },
    ]

    const { result, rerender } = renderHook(
      ({ activeId }) => useTabAlivePool(activeId, tabs),
      { initialProps: { activeId: 'a' as string } },
    )
    rerender({ activeId: 'b' })
    expect(result.current.aliveIds).toContain('a')

    // Bump version
    act(() => useUISettingsStore.getState().bumpTerminalSettingsVersion())
    rerender({ activeId: 'b' })
    // Pool rebuilt: only active tab (history cleared)
    expect(result.current.poolVersion).toBe(1)
  })
})
