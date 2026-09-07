// spa/src/stores/useAgentStore.provenance.test.ts — the SPA write path reads
// ONLY `detail.pdx_provenance` (spec §4.3.1).
import { describe, it, expect, beforeEach } from 'vitest'
import { useAgentStore, type NormalizedEvent } from './useAgentStore'
import { useTabStore } from './useTabStore'
import { createTab } from '../types/tab'

/** Seed a single-pane tab bound to (h1, abc123) at the given generation. */
function seedTerminalPane(tmuxInstance: string) {
  const tab = createTab({
    kind: 'tmux-session', hostId: 'h1', sessionCode: 'abc123',
    mode: 'terminal', cachedName: 'dev', tmuxInstance,
  })
  useTabStore.setState({ tabs: { [tab.id]: tab }, tabOrder: [tab.id], activeTabId: tab.id })
  return tab
}

/** Read the rebuild record off that tab's single leaf pane. */
function recordOf(tabId: string) {
  const l = useTabStore.getState().tabs[tabId].layout
  return l.type === 'leaf' && l.pane.content.kind === 'tmux-session' ? l.pane.content.rebuild : undefined
}

const envelope = (over?: Record<string, unknown>) => ({
  owner_session_start: true, agent_type: 'codex', session_id: 'S1',
  cwd: '/w/p', tmux_pane_id: '%2', tmux_instance: '222:2000', ...over,
})

const event = (over: Partial<NormalizedEvent>): NormalizedEvent => ({
  agent_type: 'codex', status: 'idle', raw_event_name: 'PdxSessionStart',
  broadcast_ts: 1, subagents: [], ...over,
})

const send = (e: NormalizedEvent) =>
  useAgentStore.getState().handleNormalizedEvent('h1', 'abc123', e)

beforeEach(() => {
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  useAgentStore.setState({ statuses: {}, agentTypes: {}, models: {}, subagents: {}, lastEvents: {}, unread: {} })
})

describe('provenance write path', () => {
  it('writes the pane record on an owner session start', () => {
    const tab = seedTerminalPane('222:2000')
    send(event({ detail: { pdx_provenance: envelope() } }))
    expect(recordOf(tab.id)?.resumeCommand).toBe('codex resume S1')
    expect(recordOf(tab.id)?.agent).toMatchObject({ type: 'codex', sessionId: 'S1', tmuxPaneId: '%2' })
    expect(recordOf(tab.id)?.cwd).toBe('/w/p')
    expect(recordOf(tab.id)?.cwdSource).toBe('agent-session-start')
    expect(recordOf(tab.id)?.tmuxInstance).toBe('222:2000')
  })

  it('writes nothing for a proxy-collapsed event', () => {
    const tab = seedTerminalPane('222:2000')
    send(event({ agent_type: 'cc', detail: {} }))
    expect(recordOf(tab.id)).toBeUndefined()
  })

  it('never re-derives the agent type from the outer field', () => {
    // Outer agent_type is the session-projection winner; the envelope names
    // the sender. Only the envelope may reach the record (spec §4.3.1).
    const tab = seedTerminalPane('222:2000')
    send(event({ agent_type: 'cc', detail: { pdx_provenance: envelope() } }))
    expect(recordOf(tab.id)?.agent?.type).toBe('codex')
    expect(recordOf(tab.id)?.resumeCommand).toBe('codex resume S1')
  })

  it('ignores an envelope stamped with another generation', () => {
    const tab = seedTerminalPane('111:1000')
    send(event({ detail: { pdx_provenance: envelope() } }))
    expect(recordOf(tab.id)).toBeUndefined()
  })

  it('records an unknown agent without inventing a resume command', () => {
    const tab = seedTerminalPane('222:2000')
    send(event({ detail: { pdx_provenance: envelope({ agent_type: 'aider' }) } }))
    expect(recordOf(tab.id)?.agent?.type).toBe('aider')
    expect(recordOf(tab.id)?.resumeCommand).toBeUndefined()
  })

  it('leaves cwd unset when the envelope carries none', () => {
    const tab = seedTerminalPane('222:2000')
    send(event({ detail: { pdx_provenance: envelope({ cwd: '' }) } }))
    expect(recordOf(tab.id)?.cwd).toBeUndefined()
    expect(recordOf(tab.id)?.cwdSource).toBeUndefined()
  })

  it('still handles a clear event with an envelope attached', () => {
    const tab = seedTerminalPane('222:2000')
    send(event({ status: 'clear', detail: { pdx_provenance: envelope() } }))
    expect(recordOf(tab.id)).toBeUndefined()
  })
})

describe('unverified flagging', () => {
  const seedWithAgent = () => {
    const tab = seedTerminalPane('222:2000')
    send(event({ detail: { pdx_provenance: envelope() } }))
    return tab
  }

  it('flags a record whose agent disagrees with the reconnect projection', () => {
    const tab = seedWithAgent()
    send(event({ agent_type: 'cc', raw_event_name: 'replay', detail: {} }))
    expect(recordOf(tab.id)?.unverified).toBe(true)
    expect(recordOf(tab.id)?.agent?.type).toBe('codex')
    expect(recordOf(tab.id)?.resumeCommand).toBe('codex resume S1')
  })

  it('does not flag when the projection agrees', () => {
    const tab = seedWithAgent()
    send(event({ agent_type: 'codex', raw_event_name: 'replay', detail: {} }))
    expect(recordOf(tab.id)?.unverified).toBeUndefined()
  })

  it('does not flag a record that has no agent yet', () => {
    const tab = seedTerminalPane('222:2000')
    useTabStore.getState().setPaneRebuild('h1', 'abc123', '222:2000', { kind: 'probe-cwd', cwd: '/w/p' })
    send(event({ agent_type: 'cc', raw_event_name: 'replay', detail: {} }))
    expect(recordOf(tab.id)?.unverified).toBeUndefined()
  })

  it('only the reconnect projection flags — a live event never does', () => {
    // A hot-path event's outer agent_type can name another pane's agent in the
    // same tmux session; that is not evidence the record is stale.
    const tab = seedWithAgent()
    send(event({ agent_type: 'cc', raw_event_name: 'PdxStop', detail: {} }))
    expect(recordOf(tab.id)?.unverified).toBeUndefined()
  })

  it('a fresh qualifying agent group clears the flag again', () => {
    const tab = seedWithAgent()
    send(event({ agent_type: 'cc', raw_event_name: 'replay', detail: {} }))
    expect(recordOf(tab.id)?.unverified).toBe(true)
    send(event({ detail: { pdx_provenance: envelope({ agent_type: 'cc', session_id: 'S9' }) } }))
    expect(recordOf(tab.id)?.unverified).toBeUndefined()
    expect(recordOf(tab.id)?.resumeCommand).toBe('claude --resume S9')
  })
})
