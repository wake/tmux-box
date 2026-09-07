import { useCallback, useEffect } from 'react'
import TerminalView from './TerminalView'
import ConversationView from './ConversationView'
import { TerminatedPane } from './TerminatedPane'
import { useSessionStore } from '../stores/useSessionStore'
import { useStreamStore } from '../stores/useStreamStore'
import { useConfigStore } from '../stores/useConfigStore'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { handoff, fetchWsTicket } from '../lib/host-api'
import { useHostStore } from '../stores/useHostStore'
import { findPane } from '../lib/pane-tree'
import { probeSessionCwd } from '../lib/rebuild/cwd-probe'
import type { PaneRendererProps } from '../lib/module-registry'

const EMPTY_PRESETS: Array<{ name: string; command: string }> = []

export function SessionPaneContent({ pane, isActive }: PaneRendererProps) {
  const content = pane.content
  const sessionCode = content.kind === 'tmux-session' ? content.sessionCode : ''
  const hostId = content.kind === 'tmux-session' ? content.hostId : ''
  const mode = content.kind === 'tmux-session' ? content.mode : 'terminal'
  const tmuxInstance = content.kind === 'tmux-session' ? content.tmuxInstance : ''
  const terminated = content.kind === 'tmux-session' ? content.terminated : undefined

  const wsBase = useHostStore((s) => s.getWsBase(hostId))
  const fetchHost = useSessionStore((s) => s.fetchHost)
  const streamPresets = useConfigStore((s) => s.config?.stream?.presets ?? EMPTY_PRESETS)

  const session = useSessionStore((s) =>
    (s.sessions[hostId] ?? []).find((sess) => sess.code === sessionCode) ?? null,
  )

  const handleHandoff = useCallback(async () => {
    if (!session) return
    try {
      const preset = streamPresets[0]?.name ?? 'cc'
      useStreamStore.getState().setHandoffProgress(hostId, session.code, 'starting')
      await handoff(hostId, session.code, 'stream', preset)
      await fetchHost(hostId)
    } catch (e) {
      console.error('Handoff failed:', e)
      useStreamStore.getState().setHandoffProgress(hostId, session.code, '')
    }
  }, [session, hostId, fetchHost, streamPresets])

  const handleHandoffToTerm = useCallback(async () => {
    if (!session) return
    try {
      useStreamStore.getState().setHandoffProgress(hostId, session.code, 'starting')
      await handoff(hostId, session.code, 'terminal')
      await fetchHost(hostId)
    } catch (e) {
      console.error('Handoff to term failed:', e)
      useStreamStore.getState().setHandoffProgress(hostId, session.code, '')
    }
  }, [session, hostId, fetchHost])

  // Second of the two cwd-probe triggers (spec §4.4): a pane opened after the
  // session list has settled gets no further `sessions` broadcast, so it would
  // otherwise never learn its own directory. Once per binding — the probe
  // itself deduplicates against the sessions-branch sweep.
  useEffect(() => {
    if (mode !== 'terminal' || terminated) return
    probeSessionCwd(hostId, sessionCode, tmuxInstance)
  }, [hostId, sessionCode, tmuxInstance, mode, terminated])

  // Look up tabId from store (pane renderers don't receive tabId as a prop)
  const tabId = useTabStore((s) => {
    for (const id of Object.keys(s.tabs)) {
      if (findPane(s.tabs[id].layout, pane.id)) return id
    }
    return ''
  })

  // Link-source workspace for terminal link openers (PR-5): use the workspace
  // that owns this tab, not active workspace. `undefined` for standalone tabs.
  const workspaceId = useWorkspaceStore((s) =>
    tabId ? s.findWorkspaceByTab(tabId)?.id : undefined,
  ) ?? undefined

  if (content.kind === 'tmux-session' && content.terminated) {
    return <TerminatedPane content={content} tabId={tabId} paneId={pane.id} />
  }

  if (content.kind !== 'tmux-session') return null

  if (mode === 'stream') {
    return (
      <ConversationView
        hostId={hostId}
        sessionCode={sessionCode}
        isActive={isActive}
        onHandoff={handleHandoff}
        onHandoffToTerm={handleHandoffToTerm}
      />
    )
  }

  return (
    <TerminalView
      key={`${pane.id}-${mode}`}
      wsUrl={`${wsBase}/ws/terminal/${encodeURIComponent(sessionCode)}`}
      visible={isActive}
      hostId={hostId}
      sessionCode={sessionCode}
      workspaceId={workspaceId}
      getTicket={() => fetchWsTicket(hostId)}
    />
  )
}
