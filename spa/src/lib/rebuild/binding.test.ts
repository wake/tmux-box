// spa/src/lib/rebuild/binding.test.ts — the two binding comparisons (§4.5).
import { describe, it, expect } from 'vitest'
import { bindingEquals, bindingMatchesLegacy, generationMatchesLegacy } from './binding'

const b = (tmuxInstance = '111:1000', sessionCode = 'abc', hostId = 'h1') =>
  ({ hostId, sessionCode, tmuxInstance })

describe('bindingEquals', () => {
  it('is exact in every field, generation included', () => {
    expect(bindingEquals(b(), b())).toBe(true)
    expect(bindingEquals(b('222:2000'), b())).toBe(false)
    expect(bindingEquals(b('111:1000', 'other'), b())).toBe(false)
    expect(bindingEquals(b('111:1000', 'abc', 'h2'), b())).toBe(false)
  })

  it('treats an unknown generation as a value, not a wildcard', () => {
    // A pane that has since learnt its generation has MOVED, as far as an
    // operation planned from the unknown one is concerned.
    expect(bindingEquals(b(''), b('111:1000'))).toBe(false)
    expect(bindingEquals(b('111:1000'), b(''))).toBe(false)
    expect(bindingEquals(b(''), b(''))).toBe(true)
  })
})

describe('bindingMatchesLegacy', () => {
  it('lets a pane with no recorded generation answer to any expected one', () => {
    expect(bindingMatchesLegacy(b(''), b('222:2000'))).toBe(true)
    expect(bindingMatchesLegacy(b('111:1000'), b('111:1000'))).toBe(true)
    expect(bindingMatchesLegacy(b('111:1000'), b('222:2000'))).toBe(false)
  })

  it('does not make an unknown EXPECTED generation a broadcast to everyone', () => {
    expect(bindingMatchesLegacy(b('111:1000'), b(''))).toBe(false)
    expect(bindingMatchesLegacy(b(''), b(''))).toBe(true)
  })

  it('still requires host and code to be exact', () => {
    expect(bindingMatchesLegacy(b('', 'other'), b())).toBe(false)
    expect(bindingMatchesLegacy(b('', 'abc', 'h2'), b())).toBe(false)
  })
})

describe('generationMatchesLegacy', () => {
  it('is the generation half of the legacy rule', () => {
    expect(generationMatchesLegacy('', '222:2000')).toBe(true)
    expect(generationMatchesLegacy('111:1000', '111:1000')).toBe(true)
    expect(generationMatchesLegacy('111:1000', '')).toBe(false)
  })
})
