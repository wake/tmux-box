import { describe, it, expect, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useIsMobile } from './useIsMobile'

describe('useIsMobile', () => {
  it('returns false for wide viewport', () => {
    vi.stubGlobal('matchMedia', vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    })))

    const { result } = renderHook(() => useIsMobile())
    expect(result.current).toBe(false)

    vi.unstubAllGlobals()
  })

  it('returns true for narrow viewport', () => {
    vi.stubGlobal('matchMedia', vi.fn().mockImplementation((query: string) => ({
      matches: true,
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    })))

    const { result } = renderHook(() => useIsMobile())
    expect(result.current).toBe(true)

    vi.unstubAllGlobals()
  })
})
