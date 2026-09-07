// spa/src/hooks/useMultiHostEventWs.generation.test.ts — behaviour of the
// sessions handler, exercised through the extracted pure function.
import { describe, it, expect } from 'vitest'
import { reconcileSessionsPayload } from '../lib/rebuild/reconcile'

const pane = (tmuxInstance: string, sessionCode = 'abc123') => ({
  hostId: 'h1', sessionCode, tmuxInstance,
})

describe('reconcileSessionsPayload', () => {
  it('marks tmux-restarted even when the code is present in the live list', () => {
    const out = reconcileSessionsPayload({
      hostId: 'h1',
      sessions: [{ code: 'abc123', name: 'dev', tmux_instance: '222:2000' }],
      panes: [pane('111:1000')],
    })
    expect(out.terminate).toEqual([
      { hostId: 'h1', sessionCode: 'abc123', expectedTmuxInstance: '111:1000', reason: 'tmux-restarted' },
    ])
  })

  it('marks nothing when either side is unknown', () => {
    for (const [recorded, live] of [['', '222:2000'], ['111:1000', ''], ['', '']]) {
      const out = reconcileSessionsPayload({
        hostId: 'h1',
        sessions: [{ code: 'abc123', name: 'dev', tmux_instance: live }],
        panes: [pane(recorded)],
      })
      expect(out.terminate).toEqual([])
    }
  })

  it('marks nothing when the payload omits the field entirely', () => {
    const out = reconcileSessionsPayload({
      hostId: 'h1',
      sessions: [{ code: 'abc123', name: 'dev' }],
      panes: [pane('111:1000')],
    })
    expect(out.terminate).toEqual([])
    expect(out.adoptInstance).toEqual([])
  })

  it('marks nothing when the generation is unchanged', () => {
    const out = reconcileSessionsPayload({
      hostId: 'h1',
      sessions: [{ code: 'abc123', name: 'dev', tmux_instance: '111:1000' }],
      panes: [pane('111:1000')],
    })
    expect(out.terminate).toEqual([])
    expect(out.adoptInstance).toEqual([])
  })

  it('still marks a code missing from the live list as session-closed', () => {
    const out = reconcileSessionsPayload({ hostId: 'h1', sessions: [], panes: [pane('111:1000')] })
    expect(out.terminate[0].reason).toBe('session-closed')
  })

  it('carries the pane generation on a session-closed decision', () => {
    const out = reconcileSessionsPayload({ hostId: 'h1', sessions: [], panes: [pane('111:1000'), pane('')] })
    expect(out.terminate).toEqual([
      { hostId: 'h1', sessionCode: 'abc123', expectedTmuxInstance: '111:1000', reason: 'session-closed' },
      { hostId: 'h1', sessionCode: 'abc123', expectedTmuxInstance: '', reason: 'session-closed' },
    ])
  })

  it('emits one decision per distinct generation and de-duplicates the rest', () => {
    const out = reconcileSessionsPayload({
      hostId: 'h1',
      sessions: [{ code: 'abc123', name: 'dev', tmux_instance: '333:3000' }],
      panes: [pane('111:1000'), pane('111:1000'), pane('222:2000')],
    })
    expect(out.terminate).toEqual([
      { hostId: 'h1', sessionCode: 'abc123', expectedTmuxInstance: '111:1000', reason: 'tmux-restarted' },
      { hostId: 'h1', sessionCode: 'abc123', expectedTmuxInstance: '222:2000', reason: 'tmux-restarted' },
    ])
  })

  it('adopts the live generation onto panes that have none', () => {
    const out = reconcileSessionsPayload({
      hostId: 'h1',
      sessions: [{ code: 'abc123', name: 'dev', tmux_instance: '222:2000' }],
      panes: [pane('')],
    })
    expect(out.adoptInstance).toEqual([{ sessionCode: 'abc123', tmuxInstance: '222:2000' }])
  })

  it('never adopts an empty live generation', () => {
    const out = reconcileSessionsPayload({
      hostId: 'h1',
      sessions: [{ code: 'abc123', name: 'dev', tmux_instance: '' }],
      panes: [pane('')],
    })
    expect(out.adoptInstance).toEqual([])
  })

  it('never adopts onto a code that is gone', () => {
    const out = reconcileSessionsPayload({ hostId: 'h1', sessions: [], panes: [pane('')] })
    expect(out.adoptInstance).toEqual([])
  })

  it('ignores panes belonging to another host', () => {
    const out = reconcileSessionsPayload({
      hostId: 'h1',
      sessions: [{ code: 'abc123', name: 'dev', tmux_instance: '222:2000' }],
      panes: [{ hostId: 'h2', sessionCode: 'abc123', tmuxInstance: '111:1000' }],
    })
    expect(out.terminate).toEqual([])
    expect(out.adoptInstance).toEqual([])
  })
})
