// spa/src/hooks/useMultiHostEventWs.ts — Multi-host event WS + connection state machine
import { useEffect, useRef } from 'react'
import { useHostStore, type HostRuntime } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useStreamStore } from '../stores/useStreamStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useTabStore } from '../stores/useTabStore'
import { connectHostEvents, type EventConnection } from '../lib/host-events'
import { dispatchAgentWsEvent, isAgentWsEvent } from '../lib/agent-ws'
import { dispatchBackupWsEvent } from '../lib/storage-backup/backup-ws-dispatch'
import { usePathCacheStore } from '../stores/path-cache/usePathCacheStore'
import { debugStatuslineTest } from '../lib/statusline-test-debug'
import { scanPaneTree } from '../lib/pane-tree'
import { reconcileSessionsPayload, type ReconcilePane } from '../lib/rebuild/reconcile'
import { closeAttachGate, openAttachGate } from '../lib/rebuild/attach-gate'
import { probeMissingCwds } from '../lib/rebuild/cwd-probe'
import { hostWsUrl, fetchWsTicket, fetchHistory, type Session } from '../lib/host-api'
import { checkHealth, type HealthResult } from '../lib/host-connection'
import { ConnectionStateMachine } from '../lib/connection-state-machine'

interface HostEntry {
  conn: EventConnection
  sm: ConnectionStateMachine
  configKey: string // "ip:port"
}

export function useMultiHostEventWs() {
  const hostConfigKey = useHostStore((s) =>
    s.hostOrder.map((id) => {
      const h = s.hosts[id]
      return h ? `${id}:${h.ip}:${h.port}` : id
    }).join(',')
  )

  const entriesRef = useRef(new Map<string, HostEntry>())

  useEffect(() => {
    const { hosts, hostOrder } = useHostStore.getState()
    const entries = entriesRef.current
    const currentIds = new Set(hostOrder)

    // 1. Remove hosts no longer in hostOrder
    for (const [hostId, entry] of entries) {
      if (!currentIds.has(hostId)) {
        entry.conn.close()
        entry.sm.stop()
        entries.delete(hostId)
      }
    }

    // 2. For each host, check if configKey changed; skip if same
    for (const hostId of hostOrder) {
      const host = hosts[hostId]
      if (!host) continue

      const configKey = `${host.ip}:${host.port}`
      const existing = entries.get(hostId)

      if (existing && existing.configKey === configKey) {
        continue // no change — keep existing connection
      }

      // Teardown old entry if config changed
      if (existing) {
        existing.conn.close()
        existing.sm.stop()
      }

      // Create new SM + WS for this host
      const wsUrl = hostWsUrl(hostId, '/ws/host-events')
      const baseUrl = useHostStore.getState().getDaemonBase(hostId)

      const connRef: { current: EventConnection | undefined } = { current: undefined }

      const statusMap: Record<HealthResult['daemon'], HostRuntime['status']> = {
        connected: 'connected',
        unreachable: 'disconnected',
        refused: 'disconnected',
        'auth-error': 'auth-error',
      }

      const sm = new ConnectionStateMachine(
        () => checkHealth(baseUrl, () => useHostStore.getState().hosts[hostId]?.token ?? undefined),
        (result) => {
          useHostStore.getState().setRuntime(hostId, {
            status: statusMap[result.daemon],
            latency: result.latency ?? undefined,
            daemonState: result.daemon,
          })
          // On recovery → reconnect WS with pre-fetched ticket
          if (result.daemon === 'connected' && connRef.current) {
            // A new connection starts here; nothing it will say has arrived
            // yet, so the pane's binding is unverified again (spec §4.6).
            closeAttachGate(hostId)
            if (result.ticket) {
              connRef.current.reconnectWithTicket(result.ticket)
            } else {
              connRef.current.reconnect()
            }
          }
        },
      )
      useHostStore.getState().setRuntime(hostId, { manualRetry: () => sm.trigger() })

      // --- WS connection (per host) ---
      const conn = connectHostEvents(
        wsUrl,
        (event) => {
          if (event.type === 'sessions') {
            try {
              const data: Session[] = JSON.parse(event.value)

              // Decide BEFORE mutating anything (spec §4.5): the verdict must
              // read the panes as they were when the payload arrived, not
              // after a name refresh has already touched them.
              const panes: ReconcilePane[] = []
              for (const tab of Object.values(useTabStore.getState().tabs)) {
                scanPaneTree(tab.layout, (pane) => {
                  const c = pane.content
                  if (c.kind === 'tmux-session' && c.hostId === hostId && !c.terminated) {
                    panes.push({ hostId: c.hostId, sessionCode: c.sessionCode, tmuxInstance: c.tmuxInstance })
                  }
                })
              }
              const outcome = reconcileSessionsPayload({ hostId, sessions: data, panes })

              useSessionStore.getState().replaceHost(hostId, data)

              // Adoption first — a pane that takes the live generation here is
              // no longer matched by a sibling's tmux-restarted decision.
              for (const { sessionCode, tmuxInstance } of outcome.adoptInstance) {
                useTabStore.getState().adoptTmuxInstance(hostId, sessionCode, tmuxInstance)
              }
              for (const s of data) {
                useTabStore.getState().updateSessionCache(hostId, s.code, s.name, s.tmux_instance ?? '')
              }

              const clearedCodes = new Set<string>()
              for (const { sessionCode, expectedTmuxInstance, reason } of outcome.terminate) {
                useTabStore.getState().markTerminatedForGeneration(hostId, sessionCode, expectedTmuxInstance, reason)
                if (reason !== 'session-closed' || clearedCodes.has(sessionCode)) continue
                clearedCodes.add(sessionCode)
                // Clear agent state (subagents, status, etc.) so indicators
                // don't linger after the tmux session disappears.
                useAgentStore.getState().clearSession(hostId, sessionCode)
                // Path-cache entries tagged with this sessionCode are now
                // dead (their owning agent session is gone); other sessions
                // sharing the same cwd keep their entries. SessionCode is
                // host-local so we must scope the clear by hostId — sibling
                // hosts can mint identical codes (R3 P2).
                usePathCacheStore.getState().clearBySession(hostId, sessionCode)
              }

              // This connection has now told us which generation is live and
              // the panes have been reconciled against it: terminals bound to
              // this host may attach (spec §4.6). A payload we could not parse
              // is no evidence, so this stays inside the try.
              openAttachGate(hostId)

              // First of the two cwd-probe triggers (spec §4.4). Runs after
              // reconciliation so a pane about to be marked dead, or one that
              // just adopted this generation, is judged on its final binding.
              probeMissingCwds(hostId)
            } catch { /* ignore */ }
            return
          }
          if (event.type === 'hook') {
            try {
              const hookData = JSON.parse(event.value)
              useAgentStore.getState().handleNormalizedEvent(hostId, event.session, hookData)
            } catch { /* ignore */ }
          }
          if (event.type === 'relay') {
            useStreamStore.getState().setRelayStatus(hostId, event.session, event.value === 'connected')
          }
          if (event.type === 'tmux') {
            useHostStore.getState().setRuntime(hostId, {
              tmuxState: event.value === 'ok' ? 'ok' : 'unavailable',
            })
          }
          if (event.type === 'backup:done') {
            // Cross-device backup refresh (spec §4.6). The dispatch helper
            // ignores this device's own posts (already reflected by backupNow).
            dispatchBackupWsEvent(hostId, event)
            return
          }
          if (event.type === 'handoff') {
            const store = useStreamStore.getState()
            if (event.value === 'connected') {
              store.setHandoffProgress(hostId, event.session, '')
              useSessionStore.getState().fetchHost(hostId).then(() => {
                const sess = (useSessionStore.getState().sessions[hostId] ?? [])
                  .find((s) => s.code === event.session)
                if (sess && sess.mode !== 'terminal') {
                  fetchHistory(hostId, sess.code).then((msgs) => {
                    useStreamStore.getState().loadHistory(hostId, event.session, msgs)
                  }).catch(() => {})
                } else {
                  useStreamStore.getState().clearSession(hostId, event.session)
                }
              }).catch(() => {})
            } else if (event.value.startsWith('failed')) {
              store.setHandoffProgress(hostId, event.session, '')
              useSessionStore.getState().fetchHost(hostId).catch(() => {})
            } else {
              store.setHandoffProgress(hostId, event.session, event.value)
            }
          }
          // Whitelist sourced from agent-ws/index.ts — single source so adding
          // a new agent.* handler only requires updating AGENT_WS_EVENT_TYPES
          // (R2-F2). No broad startsWith filter (defender review #9).
          if (isAgentWsEvent(event.type)) {
            if (event.session.startsWith('__pdx_test_')) {
              debugStatuslineTest('ws.entry', {
                hostId,
                type: event.type,
                session: event.session,
                valueLen: event.value.length,
              })
            }
            dispatchAgentWsEvent(hostId, event)
            return
          }
        },
        // onClose — trigger SM health check (no auto-reconnect)
        () => {
          // Immediately, not on the next open: from here on nothing confirms
          // the panes' bindings (spec §4.6).
          useHostStore.getState().setRuntime(hostId, { status: 'reconnecting', attachReady: false })
          sm.trigger()
        },
        // onOpen
        () => {
          useHostStore.getState().setRuntime(hostId, {
            status: 'connected',
            daemonState: 'connected',
          })
          useSessionStore.getState().fetchHost(hostId).catch(() => {})
        },
        () => fetchWsTicket(hostId),
        false, // autoReconnect disabled — SM manages reconnection
        true,  // lazy — waits for SM to trigger first connection
      )
      connRef.current = conn

      entries.set(hostId, { conn, sm, configKey })

      // A brand-new connection: the gate starts closed and only this
      // connection's own `sessions` payload may open it.
      closeAttachGate(hostId)

      // Start negotiation — SM will trigger reconnectWithTicket on success
      sm.trigger()
    }
    // No cleanup here — diff logic handles teardown of changed/removed hosts.
    // Unmount cleanup is handled by the separate effect below.
  }, [hostConfigKey])

  // Unmount-only cleanup: close all entries when the component unmounts
  useEffect(() => {
    const entries = entriesRef.current
    return () => {
      entries.forEach((entry) => {
        entry.conn.close()
        entry.sm.stop()
      })
      entries.clear()
    }
  }, [])
}
