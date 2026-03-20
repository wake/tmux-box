import { describe, it, expect } from 'vitest'
import { parseHash } from './hash-routing'

describe('parseHash', () => {
  it('returns null tabId for empty hash', () => {
    window.location.hash = ''
    expect(parseHash().tabId).toBeNull()
  })

  it('parses new format #/tab/{id}', () => {
    window.location.hash = '#/tab/abc-123'
    expect(parseHash()).toEqual({ tabId: 'abc-123' })
  })

  it('returns null tabId for #/tab/ with empty id', () => {
    window.location.hash = '#/tab/'
    expect(parseHash().tabId).toBeNull()
  })

  it('returns null for unknown format', () => {
    window.location.hash = '#/something/else'
    expect(parseHash().tabId).toBeNull()
  })
})
