import { useCallback } from 'react'
import TerminalView from './TerminalView'
import ConversationView from './ConversationView'
import { getSessionName, getSessionCode } from '../lib/tab-helpers'
import { useSessionStore } from '../stores/useSessionStore'
import { useStreamStore } from '../stores/useStreamStore'
import { useConfigStore } from '../stores/useConfigStore'
import { handoff } from '../lib/api'
import type { TabRendererProps } from '../lib/tab-registry'

const EMPTY_PRESETS: Array<{ name: string; command: string }> = []

export function SessionTabContent({ tab, isActive, wsBase, daemonBase }: TabRendererProps) {
  const sessionName = getSessionName(tab)
  const sessionCode = getSessionCode(tab)
  const viewMode = tab.viewMode ?? 'terminal'
  const fetchSessions = useSessionStore((s) => s.fetch)
  const streamPresets = useConfigStore((s) => s.config?.stream?.presets ?? EMPTY_PRESETS)

  const session = useSessionStore((s) =>
    s.sessions.find((sess) => sess.name === sessionName) ?? null,
  )

  const handleHandoff = useCallback(async () => {
    if (!session) return
    try {
      const preset = streamPresets[0]?.name ?? 'cc'
      useStreamStore.getState().setHandoffProgress(session.name, 'starting')
      await handoff(daemonBase, session.code, 'stream', preset)
      await fetchSessions(daemonBase)
    } catch (e) {
      console.error('Handoff failed:', e)
      useStreamStore.getState().setHandoffProgress(session.name, '')
    }
  }, [session, daemonBase, fetchSessions, streamPresets])

  const handleHandoffToTerm = useCallback(async () => {
    if (!session) return
    try {
      useStreamStore.getState().setHandoffProgress(session.name, 'starting')
      await handoff(daemonBase, session.code, 'term')
      await fetchSessions(daemonBase)
    } catch (e) {
      console.error('Handoff to term failed:', e)
      useStreamStore.getState().setHandoffProgress(session.name, '')
    }
  }, [session, daemonBase, fetchSessions])

  if (!sessionName || !sessionCode) {
    return (
      <div className="flex-1 flex items-center justify-center text-gray-600 text-sm">
        No session
      </div>
    )
  }

  if (viewMode === 'stream') {
    return (
      <ConversationView
        sessionName={sessionName}
        onHandoff={handleHandoff}
        onHandoffToTerm={handleHandoffToTerm}
      />
    )
  }

  return (
    <TerminalView
      key={`${tab.id}-${viewMode}`}
      wsUrl={`${wsBase}/ws/terminal/${encodeURIComponent(sessionCode)}`}
      visible={isActive}
    />
  )
}
