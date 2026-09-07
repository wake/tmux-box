import { describe, it, expect } from 'vitest'
import { parseProvenance } from './provenance'

const envelope = {
  owner_session_start: true, agent_type: 'codex', session_id: 'S1',
  cwd: '/w/p', tmux_pane_id: '%2', tmux_instance: '222:2000',
}

describe('parseProvenance', () => {
  it('parses a well-formed envelope', () => {
    expect(parseProvenance({ pdx_provenance: envelope })).toEqual({
      agentType: 'codex', sessionId: 'S1', cwd: '/w/p',
      tmuxPaneId: '%2', tmuxInstance: '222:2000',
    })
  })

  it('returns null when the flag is absent or false', () => {
    expect(parseProvenance({ pdx_provenance: { ...envelope, owner_session_start: false } })).toBeNull()
    expect(parseProvenance({ pdx_provenance: { ...envelope, owner_session_start: 'true' } })).toBeNull()
    expect(parseProvenance({ pdx_provenance: { ...envelope, owner_session_start: 1 } })).toBeNull()
    expect(parseProvenance({ pdx_provenance: { agent_type: 'cc', tmux_instance: '1:1' } })).toBeNull()
    expect(parseProvenance({ agent_type: 'cc', session_id: 'S1' })).toBeNull()
    expect(parseProvenance(undefined)).toBeNull()
  })

  it('returns null when the generation is unknown', () => {
    expect(parseProvenance({ pdx_provenance: { ...envelope, tmux_instance: '' } })).toBeNull()
    expect(parseProvenance({ pdx_provenance: { ...envelope, tmux_instance: undefined } })).toBeNull()
  })

  it('returns null when the agent type is unknown', () => {
    expect(parseProvenance({ pdx_provenance: { ...envelope, agent_type: '' } })).toBeNull()
    expect(parseProvenance({ pdx_provenance: { ...envelope, agent_type: 7 } })).toBeNull()
  })

  it('ignores a non-object envelope', () => {
    expect(parseProvenance({ pdx_provenance: 'yes' })).toBeNull()
    expect(parseProvenance({ pdx_provenance: null })).toBeNull()
    expect(parseProvenance({ pdx_provenance: [envelope] })).toBeNull()
  })

  it('defaults the optional fields rather than trusting their types', () => {
    expect(parseProvenance({
      pdx_provenance: { owner_session_start: true, agent_type: 'cc', tmux_instance: '1:1' },
    })).toEqual({ agentType: 'cc', sessionId: '', cwd: '', tmuxPaneId: '', tmuxInstance: '1:1' })
    expect(parseProvenance({
      pdx_provenance: { ...envelope, session_id: 42, cwd: {}, tmux_pane_id: false },
    })).toEqual({ agentType: 'codex', sessionId: '', cwd: '', tmuxPaneId: '', tmuxInstance: '222:2000' })
  })
})
