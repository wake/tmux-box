// spa/src/stores/useAgentStore.ts
import { create } from 'zustand'
import { getActiveSessionInfo } from '../lib/active-session'
import { compositeKey } from '../lib/composite-key'
import { parseProvenance } from '../lib/rebuild/provenance'
import { useTabStore } from './useTabStore'
import { scanPaneTree } from '../lib/pane-tree'

export type AgentStatus = 'running' | 'waiting' | 'idle' | 'error'

/**
 * OSC 0/2 payloads are technically free-form — agents / shells occasionally
 * embed ANSI CSI (colours) or C0 control chars that xterm passes through raw.
 * Strip them so the text is safe to render in React and in native `title=""`
 * tooltips.
 */
export function sanitizeOscTitle(raw: string): string {
  // eslint-disable-next-line no-control-regex
  return raw.replace(/\x1b\[[\d;]*[A-Za-z]/g, '').replace(/[\x00-\x1f\x7f]/g, '').trim()
}

/**
 * Return a new record containing every entry of `rec` except those whose key
 * matches `shouldOmit`. Shared by `removeHost` (prefix match) and
 * `clearHostAgentStatus` (set membership).
 */
function omitKeys<T>(
  rec: Record<string, T>,
  shouldOmit: (key: string) => boolean,
): Record<string, T> {
  const out: Record<string, T> = {}
  for (const [k, v] of Object.entries(rec)) {
    if (!shouldOmit(k)) out[k] = v
  }
  return out
}

/**
 * Subagent reference — mirrors Go `internal/agent.SubagentRef`. Covers native
 * SubagentStart refs (source_pid=0 / source_start_time="" / is_proxy omitted)
 * and cross-agent proxy refs (source_pid/source_start_time populated +
 * is_proxy:true). Phase 2 PR-2a wires the type end-to-end; PR-2b lights up
 * proxy detection and the dot visual (type color + outline).
 */
export interface SubagentRef {
  id: string
  type: string
  started_at: number
  source_pid: number
  source_start_time: string
  is_proxy?: boolean
  delegating?: boolean
  delegating_tool_use_ids?: string[]
}

/** Normalized event from backend (replaces AgentHookEvent). */
export interface NormalizedEvent {
  agent_type: string
  status: string             // running | waiting | idle | error | clear
  model?: string
  subagents?: SubagentRef[] | null
  raw_event_name: string
  broadcast_ts: number
  detail?: Record<string, unknown>
}

/**
 * Write the rebuild record from a qualifying `SessionStart`'s provenance
 * envelope (spec §4.2 step 3). The whole agent group is replaced as one unit,
 * so a payload without `cwd` clears `cwd` rather than leaving the previous
 * agent's directory beside a new session id.
 *
 * What lands is the agent IDENTITY, never a command: `resolveResumeCommand`
 * composes from the identity at the moment the command is needed, so a
 * template the user edits later reaches every pane already recorded.
 */
function writeProvenanceRecord(
  hostId: string,
  sessionCode: string,
  detail: Record<string, unknown> | undefined,
): boolean {
  const prov = parseProvenance(detail)
  if (!prov) return false
  const now = Date.now()
  useTabStore.getState().setPaneRebuild(hostId, sessionCode, prov.tmuxInstance, {
    kind: 'agent-group',
    record: {
      tmuxInstance: prov.tmuxInstance,
      cwd: prov.cwd || undefined,
      cwdSource: prov.cwd ? 'agent-session-start' : undefined,
      agent: {
        type: prov.agentType,
        sessionId: prov.sessionId || undefined,
        tmuxPaneId: prov.tmuxPaneId || undefined,
        updatedAt: now,
      },
      capturedAt: now,
    },
  })
  return true
}

/**
 * The reconnect projection replay (`internal/module/agent/module.go:564`)
 * delivers the session's *current* agent type but no provenance. When it
 * disagrees with the record, the SPA was away while the session changed
 * agents: the record still holds the old agent and nothing else marks it
 * wrong, so flag it unverified (spec §9.1).
 *
 * Only the replay does this. A hot-path event's outer `agent_type` is the
 * session-projection winner, which can legitimately name another tmux pane of
 * the same session — not evidence that the record is stale.
 */
function flagUnverifiedAgent(hostId: string, sessionCode: string, agentType: string): void {
  if (!agentType) return
  const instances = new Set<string>()
  for (const tab of Object.values(useTabStore.getState().tabs)) {
    scanPaneTree(tab.layout, (pane) => {
      const c = pane.content
      if (c.kind !== 'tmux-session' || c.mode !== 'terminal') return
      if (c.hostId !== hostId || c.sessionCode !== sessionCode) return
      const recorded = c.rebuild?.agent?.type
      if (!recorded || recorded === agentType || c.rebuild?.unverified === true) return
      instances.add(c.tmuxInstance)
    })
  }
  for (const tmuxInstance of instances) {
    useTabStore.getState().setPaneRebuild(hostId, sessionCode, tmuxInstance, {
      kind: 'unverified', unverified: true,
    })
  }
}

/** Latest CC statusLine snapshot per session (ephemeral, from statusLine wrapper). */
export interface CcStatusEntry {
  receivedAt: number
  raw: Record<string, unknown>
}

interface AgentState {
  // Backend-derived state
  statuses: Record<string, AgentStatus>
  agentTypes: Record<string, string>
  models: Record<string, string>
  subagents: Record<string, SubagentRef[]>
  lastEvents: Record<string, NormalizedEvent>  // for notification dispatcher
  oscTitles: Record<string, string>  // latest OSC 0/2 title per session (ephemeral)
  ccStatus: Record<string, CcStatusEntry>  // latest CC statusLine snapshot (ephemeral)

  // UI state
  unread: Record<string, boolean>

  // Actions
  handleNormalizedEvent: (hostId: string, sessionCode: string, event: NormalizedEvent) => void
  clearSession: (hostId: string, sessionCode: string) => void
  markRead: (hostId: string, sessionCode: string) => void
  removeHost: (hostId: string) => void
  setOscTitle: (hostId: string, sessionCode: string, title: string) => void
  setCcStatus: (hostId: string, sessionCode: string, raw: Record<string, unknown>) => void
  clearHostAgentStatus: (hostId: string) => void
}

export const useAgentStore = create<AgentState>()(
  (set, get) => ({
    statuses: {},
    agentTypes: {},
    models: {},
    subagents: {},
    lastEvents: {},
    oscTitles: {},
    ccStatus: {},
    unread: {},

    clearSession: (hostId, sessionCode) => {
      const key = compositeKey(hostId, sessionCode)
      set((s) => {
        const filterOut = <T,>(rec: Record<string, T>): Record<string, T> => {
           
          const { [key]: _, ...rest } = rec
          return rest
        }
        return {
          statuses: filterOut(s.statuses),
          agentTypes: filterOut(s.agentTypes),
          models: filterOut(s.models),
          subagents: filterOut(s.subagents),
          lastEvents: filterOut(s.lastEvents),
          oscTitles: filterOut(s.oscTitles),
          ccStatus: filterOut(s.ccStatus),
          unread: filterOut(s.unread),
        }
      })
    },

    handleNormalizedEvent: (hostId, sessionCode, event) => {
      const key = compositeKey(hostId, sessionCode)

      if (event.status === 'clear') {
        get().clearSession(hostId, sessionCode)
        return
      }

      // Store last event (for notification dispatcher)
      set((s) => ({ lastEvents: { ...s.lastEvents, [key]: event } }))

      // Store agent type
      if (event.agent_type) {
        set((s) => ({ agentTypes: { ...s.agentTypes, [key]: event.agent_type } }))
      }

      // Rebuild record (spec §4.2). Reads ONLY `detail.pdx_provenance` — the
      // outer `agent_type` above is the session-projection winner and must
      // never reach the record (spec §4.3.1).
      const wroteProvenance = writeProvenanceRecord(hostId, sessionCode, event.detail)
      if (!wroteProvenance && event.raw_event_name === 'replay') {
        flagUnverifiedAgent(hostId, sessionCode, event.agent_type)
      }

      // Store model (persist across events)
      if (event.model) {
        set((s) => ({ models: { ...s.models, [key]: event.model! } }))
      }

      // Store subagents
      if ('subagents' in event) {
        const subagents = event.subagents ?? []
        set((s) => ({
          subagents: subagents.length > 0
            ? { ...s.subagents, [key]: subagents }
            : (() => { const { [key]: _, ...rest } = s.subagents; return rest })(),
        }))
      }

      // Store status (skip events with no status, e.g. SubagentStart/Stop)
      const status = event.status as AgentStatus | ''
      if (status) {
        set((s) => ({ statuses: { ...s.statuses, [key]: status } }))

        // running is unambiguous user activity — clear any leftover unread
        // (e.g. raised by a prior waiting/idle while the tab was in background)
        // regardless of focus.
        if (status === 'running') {
          set((s) => {
            if (!(key in s.unread)) return s

            const { [key]: _, ...rest } = s.unread
            return { unread: rest }
          })
          return
        }

        // Mark unread when not focused. Notification raises status=idle but
        // shouldn't surface as actionable: cc emits PdxNotification post-W2,
        // codex/opencode pre-migration still emit "Notification". Recognise
        // both literals during the transition.
        const rawName = event.raw_event_name
        const isNotification = rawName === 'Notification' || rawName === 'PdxNotification'
        const notificationSilent = event.detail?.notification_silent === true
        const isActionable = status === 'waiting' || status === 'error' ||
          (status === 'idle' && !isNotification && !notificationSilent)
        const activeInfo = getActiveSessionInfo()
        const activeKey = activeInfo ? compositeKey(activeInfo.hostId, activeInfo.sessionCode) : ''
        if (isActionable && activeKey !== key) {
          set((s) => ({ unread: { ...s.unread, [key]: true } }))
        }
      }
    },

    markRead: (hostId, sessionCode) => set((s) => {
      const key = compositeKey(hostId, sessionCode)
       
      const { [key]: _, ...rest } = s.unread
      return { unread: rest }
    }),

    removeHost: (hostId) => set((s) => {
      const prefix = `${hostId}:`
      const byPrefix = (k: string) => k.startsWith(prefix)
      return {
        statuses: omitKeys(s.statuses, byPrefix),
        agentTypes: omitKeys(s.agentTypes, byPrefix),
        models: omitKeys(s.models, byPrefix),
        subagents: omitKeys(s.subagents, byPrefix),
        lastEvents: omitKeys(s.lastEvents, byPrefix),
        oscTitles: omitKeys(s.oscTitles, byPrefix),
        ccStatus: omitKeys(s.ccStatus, byPrefix),
        unread: omitKeys(s.unread, byPrefix),
      }
    }),

    setOscTitle: (hostId, sessionCode, title) => set((s) => {
      const key = compositeKey(hostId, sessionCode)
      const cleaned = sanitizeOscTitle(title)
      if (!cleaned) {
         
        const { [key]: _, ...rest } = s.oscTitles
        return { oscTitles: rest }
      }
      if (s.oscTitles[key] === cleaned) return s
      return { oscTitles: { ...s.oscTitles, [key]: cleaned } }
    }),

    setCcStatus: (hostId, sessionCode, raw) => {
      const key = compositeKey(hostId, sessionCode)
      const entry: CcStatusEntry = { receivedAt: Date.now(), raw }
      set((s) => ({ ccStatus: { ...s.ccStatus, [key]: entry } }))
    },

    /**
     * Wipe CC statusLine state for a host. Terminal OSC 0/2 title state is
     * preserved because CC statusLine no longer owns title display.
     *
     * Called on the `agent.status.cleared` WS event, broadcast by the daemon
     * when the CC statusLine wrapper is uninstalled.
     *
     * Does NOT touch statuses / agentTypes / models / subagents / unread /
     * lastEvents — those track agent hook events, which are orthogonal to
     * statusLine install state. Uninstalling the wrapper does not imply the
     * CC session has ended; the hook stream may continue.
     */
    clearHostAgentStatus: (hostId) => set((s) => {
      const prefix = `${hostId}:`
      const ccKeysToRemove = Object.keys(s.ccStatus).filter((k) => k.startsWith(prefix))
      if (ccKeysToRemove.length === 0) return s
      return {
        ccStatus: omitKeys(s.ccStatus, (k) => k.startsWith(prefix)),
      }
    }),
  }),
)
