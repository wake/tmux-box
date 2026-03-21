import { createElement, type FC } from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, render, act } from '@testing-library/react'
import { useScrollOverflow } from './useScrollOverflow'

// Mock ResizeObserver (JSDOM doesn't have it)
beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class {
    observe() {}
    disconnect() {}
  })
})

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

  // Wrapper component to attach containerRef to a real DOM element
  function createWrapper() {
    let latest: ReturnType<typeof useScrollOverflow>
    const Wrapper: FC = () => {
      latest = useScrollOverflow()
      return createElement('div', { ref: latest.containerRef, 'data-testid': 'scroll-container' })
    }
    return {
      Wrapper,
      getResult: () => latest!,
    }
  }

  it('detects canScrollRight when content overflows', () => {
    const { Wrapper, getResult } = createWrapper()
    render(createElement(Wrapper))

    const el = getResult().containerRef.current!
    Object.defineProperty(el, 'scrollWidth', { value: 400, configurable: true })
    Object.defineProperty(el, 'clientWidth', { value: 200, configurable: true })
    Object.defineProperty(el, 'scrollLeft', { value: 0, configurable: true, writable: true })

    act(() => { el.dispatchEvent(new Event('scroll')) })

    expect(getResult().canScrollRight).toBe(true)
    expect(getResult().canScrollLeft).toBe(false)
  })

  it('detects canScrollLeft when scrolled right', () => {
    const { Wrapper, getResult } = createWrapper()
    render(createElement(Wrapper))

    const el = getResult().containerRef.current!
    Object.defineProperty(el, 'scrollWidth', { value: 400, configurable: true })
    Object.defineProperty(el, 'clientWidth', { value: 200, configurable: true })
    Object.defineProperty(el, 'scrollLeft', { value: 50, configurable: true, writable: true })

    act(() => { el.dispatchEvent(new Event('scroll')) })

    expect(getResult().canScrollLeft).toBe(true)
    expect(getResult().canScrollRight).toBe(true)
  })

  it('detects no scroll needed when scrolled to end', () => {
    const { Wrapper, getResult } = createWrapper()
    render(createElement(Wrapper))

    const el = getResult().containerRef.current!
    Object.defineProperty(el, 'scrollWidth', { value: 400, configurable: true })
    Object.defineProperty(el, 'clientWidth', { value: 200, configurable: true })
    Object.defineProperty(el, 'scrollLeft', { value: 200, configurable: true, writable: true })

    act(() => { el.dispatchEvent(new Event('scroll')) })

    expect(getResult().canScrollLeft).toBe(true)
    expect(getResult().canScrollRight).toBe(false)
  })

  it('detects no scroll when content fits', () => {
    const { Wrapper, getResult } = createWrapper()
    render(createElement(Wrapper))

    const el = getResult().containerRef.current!
    Object.defineProperty(el, 'scrollWidth', { value: 200, configurable: true })
    Object.defineProperty(el, 'clientWidth', { value: 200, configurable: true })
    Object.defineProperty(el, 'scrollLeft', { value: 0, configurable: true, writable: true })

    act(() => { el.dispatchEvent(new Event('scroll')) })

    expect(getResult().canScrollLeft).toBe(false)
    expect(getResult().canScrollRight).toBe(false)
  })

  it('scrollLeft calls scrollBy with negative offset', () => {
    const { Wrapper, getResult } = createWrapper()
    render(createElement(Wrapper))

    const el = getResult().containerRef.current!
    const scrollBySpy = vi.fn()
    el.scrollBy = scrollBySpy

    act(() => { getResult().scrollLeft() })

    expect(scrollBySpy).toHaveBeenCalledWith({ left: -150, behavior: 'smooth' })
  })

  it('scrollRight calls scrollBy with positive offset', () => {
    const { Wrapper, getResult } = createWrapper()
    render(createElement(Wrapper))

    const el = getResult().containerRef.current!
    const scrollBySpy = vi.fn()
    el.scrollBy = scrollBySpy

    act(() => { getResult().scrollRight() })

    expect(scrollBySpy).toHaveBeenCalledWith({ left: 150, behavior: 'smooth' })
  })
})
