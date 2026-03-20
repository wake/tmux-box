import { useEffect } from 'react'
import { useStreamStore } from '../stores/useStreamStore'
import { useSessionStore } from '../stores/useSessionStore'
import { connectSessionEvents } from '../lib/session-events'
import { fetchHistory } from '../lib/api'
import type { SessionStatus } from '../components/SessionStatusBadge'

export function useSessionEventWs(wsBase: string, daemonBase: string) {
  const fetchSessions = useSessionStore((s) => s.fetch)

  useEffect(() => {
    const conn = connectSessionEvents(
      `${wsBase}/ws/session-events`,
      (event) => {
        if (event.type === 'status') {
          useStreamStore.getState().setSessionStatus(event.session, event.value as SessionStatus)
          fetchSessions(daemonBase)
        }
        if (event.type === 'relay') {
          useStreamStore.getState().setRelayStatus(event.session, event.value === 'connected')
        }
        if (event.type === 'handoff') {
          const store = useStreamStore.getState()
          if (event.value === 'connected') {
            store.setHandoffProgress(event.session, '')
            fetchSessions(daemonBase).then(() => {
              const sess = useSessionStore.getState().sessions.find((s) => s.name === event.session)
              if (sess && sess.mode !== 'term') {
                fetchHistory(daemonBase, sess.id).then((msgs) => {
                  useStreamStore.getState().loadHistory(event.session, msgs)
                }).catch(() => { /* history fetch failed — non-critical */ })
              } else {
                useStreamStore.getState().clearSession(event.session)
              }
            }).catch(() => { /* fetchSessions failed — non-critical */ })
          } else if (event.value.startsWith('failed')) {
            store.setHandoffProgress(event.session, '')
            fetchSessions(daemonBase)
          } else {
            store.setHandoffProgress(event.session, event.value)
          }
        }
      },
    )
    return () => conn.close()
  }, [fetchSessions, daemonBase, wsBase])
}
