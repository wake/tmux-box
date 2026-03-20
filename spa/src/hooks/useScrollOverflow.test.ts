import { describe, it, expect } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useScrollOverflow } from './useScrollOverflow'

describe('useScrollOverflow', () => {
  it('returns canScrollLeft=false, canScrollRight=false when ref is null', () => {
    const { result } = renderHook(() => useScrollOverflow())
    expect(result.current.canScrollLeft).toBe(false)
    expect(result.current.canScrollRight).toBe(false)
  })

  it('returns scroll functions', () => {
    const { result } = renderHook(() => useScrollOverflow())
    expect(typeof result.current.scrollLeft).toBe('function')
    expect(typeof result.current.scrollRight).toBe('function')
  })

  it('returns a containerRef', () => {
    const { result } = renderHook(() => useScrollOverflow())
    expect(result.current.containerRef).toBeDefined()
    expect(result.current.containerRef.current).toBeNull()
  })
})
