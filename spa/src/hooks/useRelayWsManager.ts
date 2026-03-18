// spa/src/hooks/useRelayWsManager.ts
import { useEffect, useRef } from 'react'
import { useStreamStore } from '../stores/useStreamStore'
import { connectStream, type StreamMessage, type ControlRequest } from '../lib/stream-ws'

/**
 * Manages stream WS connections driven by relayStatus changes.
 * Creates a WS when relay connects, destroys it when relay disconnects.
 */
export function useRelayWsManager(wsBase: string) {
  const prevRelay = useRef<Record<string, boolean>>({})

  useEffect(() => {
    const activeConns = new Map<string, { close: () => void }>()

    const unsub = useStreamStore.subscribe(
      (s) => s.relayStatus,
      (relayStatus) => {
        for (const [session, connected] of Object.entries(relayStatus)) {
          const wasConnected = prevRelay.current[session] ?? false

          if (connected && !wasConnected) {
            // Relay just connected — create stream WS
            const conn = connectStream(
              `${wsBase}/ws/cli-bridge-sub/${encodeURIComponent(session)}`,
              (msg: StreamMessage) => {
                const store = useStreamStore.getState()
                if (msg.type === 'assistant' || msg.type === 'user') {
                  store.addMessage(session, msg)
                }
                if (msg.type === 'result' && 'total_cost_usd' in msg) {
                  store.addCost(session, (msg as { total_cost_usd?: number }).total_cost_usd || 0)
                  store.setStreaming(session, false)
                }
                if (msg.type === 'control_request') {
                  store.addControlRequest(session, msg as ControlRequest)
                }
                if (msg.type === 'system') {
                  const sys = msg as { subtype?: string; session_id?: string; model?: string }
                  if (sys.subtype === 'init') {
                    store.setSessionInfo(session, sys.session_id ?? '', sys.model ?? '')
                  }
                }
              },
              () => {
                // WS closed — clear conn (relay:disconnected event will handle UI state)
                useStreamStore.getState().setConn(session, null)
                activeConns.delete(session)
              },
            )
            useStreamStore.getState().setConn(session, conn)
            activeConns.set(session, conn)
          }

          if (!connected && wasConnected) {
            // Relay disconnected — close stream WS
            const existing = activeConns.get(session)
            existing?.close()
            useStreamStore.getState().setConn(session, null)
            activeConns.delete(session)
          }
        }

        // Clean up sessions removed from relayStatus (e.g., session deleted)
        for (const session of Object.keys(prevRelay.current)) {
          if (!(session in relayStatus)) {
            const existing = activeConns.get(session)
            existing?.close()
            useStreamStore.getState().setConn(session, null)
            activeConns.delete(session)
          }
        }

        prevRelay.current = { ...relayStatus }
      },
    )

    return () => {
      unsub()
      activeConns.forEach((conn) => conn.close())
      activeConns.clear()
    }
  }, [wsBase])
}
