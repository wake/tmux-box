import { describe, it, expect } from 'vitest'
import { composeResumeCommand } from './composer'

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
