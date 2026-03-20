import { useRef, useState, useEffect, useCallback } from 'react'

export function useScrollOverflow() {
  const containerRef = useRef<HTMLDivElement>(null)
  const [canScrollLeft, setCanScrollLeft] = useState(false)
  const [canScrollRight, setCanScrollRight] = useState(false)

  const update = useCallback(() => {
    const el = containerRef.current
    if (!el) return
    setCanScrollLeft(el.scrollLeft > 0)
    setCanScrollRight(el.scrollLeft + el.clientWidth < el.scrollWidth - 1)
  }, [])

  useEffect(() => {
    const el = containerRef.current
    if (!el) return

    update()
    el.addEventListener('scroll', update, { passive: true })
    const observer = new ResizeObserver(update)
    observer.observe(el)

    return () => {
      el.removeEventListener('scroll', update)
      observer.disconnect()
    }
  }, [update])

  const scrollLeft = useCallback(() => {
    const el = containerRef.current
    if (!el) return
    el.scrollBy({ left: -150, behavior: 'smooth' })
  }, [])

  const scrollRight = useCallback(() => {
    const el = containerRef.current
    if (!el) return
    el.scrollBy({ left: 150, behavior: 'smooth' })
  }, [])

  return { containerRef, canScrollLeft, canScrollRight, scrollLeft, scrollRight }
}
