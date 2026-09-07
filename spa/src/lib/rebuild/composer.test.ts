import { describe, it, expect } from 'vitest'
import { composeResumeCommand, resolveResumeCommand } from './composer'
import { DEFAULT_RESUME_TEMPLATES, type ResumeTemplateLookup } from '../../stores/useResumeTemplateStore'

describe('composeResumeCommand', () => {
  it.each([
    ['cc', 'S1', 'claude --resume S1'],
    ['cc', undefined, 'claude -c'],
    ['codex', 'S1', 'codex resume S1'],
    ['codex', undefined, 'codex resume --last'],
    ['opencode', 'S1', 'opencode -s S1'],
    ['opencode', undefined, 'opencode -c'],
  ])('%s / %s → %s', (agent, id, want) => {
    expect(composeResumeCommand(agent, id)).toBe(want)
  })

  it('accepts the real session id shapes of all three agents', () => {
    // cc / codex emit UUIDs; opencode emits a `ses_`-prefixed opaque string.
    expect(composeResumeCommand('cc', '01a07ace-6f2e-4c1b-9a3d-2f7b0c5e8d41')).toBe(
      'claude --resume 01a07ace-6f2e-4c1b-9a3d-2f7b0c5e8d41',
    )
    expect(composeResumeCommand('codex', '01a07ace-6f2e-4c1b-9a3d-2f7b0c5e8d41')).toBe(
      'codex resume 01a07ace-6f2e-4c1b-9a3d-2f7b0c5e8d41',
    )
    expect(composeResumeCommand('opencode', 'ses_8dfc21a0bffeAbC2LmNoPq')).toBe(
      'opencode -s ses_8dfc21a0bffeAbC2LmNoPq',
    )
  })

  it('returns empty for an unknown agent rather than guessing', () => {
    expect(composeResumeCommand('aider', 'S1')).toBe('')
    expect(composeResumeCommand('', undefined)).toBe('')
  })

  it('rejects a session id that could break out of the command', () => {
    expect(composeResumeCommand('cc', 'S1; rm -rf /')).toBe('claude -c')
    expect(composeResumeCommand('codex', '$(whoami)')).toBe('codex resume --last')
    expect(composeResumeCommand('opencode', 'ses_1 && curl evil')).toBe('opencode -c')
    expect(composeResumeCommand('cc', '')).toBe('claude -c')
    expect(composeResumeCommand('cc', 'a'.repeat(129))).toBe('claude -c')
  })
})

// === resolveResumeCommand — override → template → '' (spec §4.2) ===

/** The shipped defaults, as the store hands them out. */
const defaults: ResumeTemplateLookup = (agentType) =>
  Object.prototype.hasOwnProperty.call(DEFAULT_RESUME_TEMPLATES, agentType)
    ? DEFAULT_RESUME_TEMPLATES[agentType]
    : undefined

const rec = (agent?: { type: string; sessionId?: string }, override?: string) => ({
  ...(agent ? { agent: { ...agent, updatedAt: 1 } } : {}),
  ...(override === undefined ? {} : { resumeCommandOverride: override }),
})

describe('resolveResumeCommand — layer 2, the templates', () => {
  it.each([
    ['cc', 'S1', 'claude --resume S1'],
    ['cc', undefined, 'claude -c'],
    ['codex', 'S1', 'codex resume S1'],
    ['codex', undefined, 'codex resume --last'],
    ['opencode', 'ses_8dfc21a0bffeAbC2LmNoPq', 'opencode -s ses_8dfc21a0bffeAbC2LmNoPq'],
    ['opencode', undefined, 'opencode -c'],
  ])('%s / %s → %s', (type, sessionId, want) => {
    expect(resolveResumeCommand(rec({ type, sessionId }), defaults)).toBe(want)
  })

  it('degrades an unsafe id to the fallback rather than interpolating it', () => {
    expect(resolveResumeCommand(rec({ type: 'cc', sessionId: 'S1; rm -rf /' }), defaults)).toBe('claude -c')
    expect(resolveResumeCommand(rec({ type: 'codex', sessionId: '$(whoami)' }), defaults)).toBe('codex resume --last')
    expect(resolveResumeCommand(rec({ type: 'cc', sessionId: '' }), defaults)).toBe('claude -c')
    expect(resolveResumeCommand(rec({ type: 'cc', sessionId: 'a'.repeat(129) }), defaults)).toBe('claude -c')
  })

  it('replaces {id} at every occurrence of the exact template', () => {
    const twice: ResumeTemplateLookup = () => ({ exact: 'run {id} --log {id}.txt', fallback: 'run -c' })
    expect(resolveResumeCommand(rec({ type: 'cc', sessionId: 'S1' }), twice)).toBe('run S1 --log S1.txt')
  })

  it('leaves {id} literal in the fallback — it is used verbatim', () => {
    const literal: ResumeTemplateLookup = () => ({ exact: 'run {id}', fallback: 'run --last {id}' })
    expect(resolveResumeCommand(rec({ type: 'cc' }), literal)).toBe('run --last {id}')
    expect(resolveResumeCommand(rec({ type: 'cc', sessionId: 'no;pe' }), literal)).toBe('run --last {id}')
  })
})

describe('resolveResumeCommand — layer 1, the override', () => {
  it.each([
    ['cc', 'S1'],
    ['cc', undefined],
    ['aider', 'S1'],
    [undefined, undefined],
  ])('wins over the template for agent %s / id %s', (type, sessionId) => {
    const record = type ? rec({ type, sessionId }, 'cld-yolo -c') : rec(undefined, 'cld-yolo -c')
    expect(resolveResumeCommand(record, defaults)).toBe('cld-yolo -c')
  })

  it('is returned verbatim — no interpolation, no shape check', () => {
    expect(resolveResumeCommand(rec({ type: 'cc', sessionId: 'S1' }, 'claude --resume {id}'), defaults))
      .toBe('claude --resume {id}')
  })

  it('an empty override is not an override', () => {
    expect(resolveResumeCommand(rec({ type: 'cc', sessionId: 'S1' }, ''), defaults)).toBe('claude --resume S1')
  })
})

describe('resolveResumeCommand — layer 3, empty', () => {
  it.each([
    ['an agent with no template', rec({ type: 'aider', sessionId: 'S1' })],
    ['an agent with no template and no id', rec({ type: 'aider' })],
    ['a record with no agent', rec()],
    ['no record at all', undefined],
  ])('%s resolves to the empty string', (_label, record) => {
    expect(resolveResumeCommand(record, defaults)).toBe('')
  })

  it('does not read an inherited Object property as a template', () => {
    expect(resolveResumeCommand(rec({ type: 'constructor', sessionId: 'S1' }), defaults)).toBe('')
  })
})
