import { useCallback, useRef, useState } from 'react'
import { Plus, Play, Trash, PencilSimple, Check, X } from '@phosphor-icons/react'
import { useSessionStore } from '../../stores/useSessionStore'
import { useHostStore } from '../../stores/useHostStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../stores/useWorkspaceStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useAgentStore } from '../../stores/useAgentStore'
import { hostFetch, renameSession } from '../../lib/host-api'
import { compositeKey } from '../../lib/composite-key'
import { connectionErrorMessage } from '../../lib/host-utils'
import { CommandSlot, type SlotContext } from '../CommandSlot'
import { runHostSlot, type HostSlotContext } from '../../lib/slot-executor'
import { QUICK_COMMAND_SLOTS } from '../../lib/quick-command-slots'
import type { QuickCommand } from '../../lib/quick-command-bindings'
import type { Session } from '../../lib/host-api'

interface Props {
  hostId: string
}

/* ─── New Session Dialog ─── */

function NewSessionDialog({ hostId, onClose }: { hostId: string; onClose: () => void }) {
  const t = useI18nStore((s) => s.t)
  const [name, setName] = useState('')
  const [cwd, setCwd] = useState('~')
  const [mode, setMode] = useState('terminal')
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState('')

  const handleCreate = async () => {
    if (!name.trim()) return
    setCreating(true)
    setError('')
    try {
      const res = await hostFetch(hostId, '/api/sessions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: name.trim(), cwd, mode }),
      })
      if (!res.ok) {
        const body = await res.text().catch(() => '')
        setError(body || `HTTP ${res.status}`)
        return
      }
      onClose()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed')
    } finally {
      setCreating(false)
    }
  }

  return (
    <div className="p-4 bg-surface-secondary border border-border-default rounded-lg mb-4">
      <h3 className="text-sm font-semibold mb-3">{t('hosts.new_session')}</h3>
      <div className="space-y-2">
        <input
          placeholder={t('hosts.session_name')}
          value={name}
          onChange={(e) => setName(e.target.value)}
          className="w-full bg-surface-primary border border-border-default rounded px-2 py-1.5 text-sm text-text-primary"
          autoFocus
          onKeyDown={(e) => { if (e.key === 'Enter') handleCreate() }}
        />
        <input
          placeholder={t('hosts.session_cwd')}
          value={cwd}
          onChange={(e) => setCwd(e.target.value)}
          className="w-full bg-surface-primary border border-border-default rounded px-2 py-1.5 text-sm text-text-muted"
        />
        <select
          value={mode}
          onChange={(e) => setMode(e.target.value)}
          className="bg-surface-primary border border-border-default rounded px-2 py-1.5 text-sm text-text-primary"
        >
          <option value="terminal">terminal</option>
          <option value="stream">stream</option>
        </select>
      </div>
      {error && <p className="text-xs text-red-400 mt-2">{error}</p>}
      <div className="flex gap-2 mt-3">
        <button
          onClick={handleCreate}
          disabled={creating || !name.trim()}
          className="px-3 py-1.5 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50"
        >
          {t('hosts.create')}
        </button>
        <button
          onClick={onClose}
          className="px-3 py-1.5 rounded text-xs bg-surface-tertiary text-text-secondary cursor-pointer"
        >
          {t('common.cancel')}
        </button>
      </div>
    </div>
  )
}

/* ─── Inline Rename ─── */

function InlineRename({ hostId, session, onDone }: { hostId: string; session: Session; onDone: () => void }) {
  const [draft, setDraft] = useState(session.name)

  const handleSave = async () => {
    if (draft.trim() && draft !== session.name) {
      await renameSession(hostId, session.code, draft.trim())
    }
    onDone()
  }

  return (
    <span className="inline-flex items-center gap-1">
      <input
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        className="bg-surface-primary border border-border-default rounded px-1 py-0.5 text-sm w-32"
        autoFocus
        onBlur={handleSave}
        onKeyDown={(e) => {
          if (e.key === 'Enter') handleSave()
          if (e.key === 'Escape') onDone()
        }}
      />
      <button onClick={handleSave} className="text-green-400 cursor-pointer"><Check size={14} /></button>
      <button onClick={onDone} className="text-text-muted cursor-pointer"><X size={14} /></button>
    </span>
  )
}

/* ─── Main component ─── */

export function SessionsSection({ hostId }: Props) {
  const t = useI18nStore((s) => s.t)
  const sessions = useSessionStore((s) => s.sessions[hostId] ?? [])
  const runtime = useHostStore((s) => s.runtime[hostId])
  const isOffline = !runtime || runtime.status !== 'connected' || runtime.tmuxState === 'unavailable'
  const [showNew, setShowNew] = useState(false)
  const [renamingCode, setRenamingCode] = useState<string | null>(null)
  const [deletingCode, setDeletingCode] = useState<string | null>(null)
  const agentStatuses = useAgentStore((s) => s.statuses)

  const handleOpen = (session: Session, mode: string) => {
    const tabId = useTabStore.getState().openSingletonTab({
      kind: 'tmux-session',
      hostId,
      sessionCode: session.code,
      mode: mode as 'terminal' | 'stream',
      cachedName: session.name,
      // Generation from the session payload we are opening, never from
      // ambient host state (spec §4.5).
      tmuxInstance: session.tmux_instance ?? '',
    })
    useWorkspaceStore.getState().insertTab(tabId)
    useTabStore.getState().setActiveTab(tabId)
  }

  // Phase 1c — HOST_ACTIONS slot executor (spec §3.2 / §3.3.1).
  //
  // Finding 3 — executor-level busy guard. <CommandSlot busy> is the visual
  // disable; this ref is the synchronous race guard so a fast double-click
  // between React event ticks can't spawn two pipelines.
  const executingRef = useRef(false)
  const [executing, setExecuting] = useState(false)

  const runHostExecutor = useCallback(async (cmd: QuickCommand, ctx: SlotContext) => {
    if (executingRef.current) return
    executingRef.current = true
    setExecuting(true)

    // Finding 2 + R1 fix — snapshot the host page's owning workspace at
    // CLICK TIME, before any await. If we read activeTabId inside
    // switchToSession (which fires after createSession + executeCommand
    // resolve) the user could switch tabs/workspaces during those awaits,
    // and findWorkspaceByTab would resolve against a different host page or
    // a non-host tab — defeating the workspace-aware insertion.
    const clickActiveTabId = useTabStore.getState().activeTabId
    const owningWsId = clickActiveTabId
      ? useWorkspaceStore.getState().findWorkspaceByTab(clickActiveTabId)?.id ?? null
      : null

    try {
      // Finding 1 + Type-lock — narrow the SlotContext (nullable hostId) to
      // HostSlotContext (required hostId) using the prop value as the source
      // of truth. The prop hostId is string (non-null), so this is a valid
      // narrowing, not an unsafe cast.
      const hostCtx: HostSlotContext = { hostId, cwd: ctx.cwd }
      await runHostSlot(cmd, hostCtx, {
        switchToSession: (h, sessionCode) => {
          const tabId = useTabStore.getState().openSingletonTab({
            kind: 'tmux-session',
            hostId: h,
            sessionCode,
            mode: 'terminal',
            cachedName: sessionCode,
            // No Session payload in hand here (the slot executor hands back a
            // bare code), so the generation stays unknown; the next sessions
            // payload adopts it.
            tmuxInstance: '',
          })
          // Explicit null when host page was standalone at click time —
          // bypasses the activeWorkspaceId fallback in insertTab so a stale
          // activeWorkspaceId (or a workspace switch during createSession)
          // doesn't redirect the new session.
          useWorkspaceStore.getState().insertTab(tabId, owningWsId)
          useTabStore.getState().setActiveTab(tabId)
        },
        // Finding 1 — host liveness probe. Reads the latest store snapshot
        // every call (incl. retry) so a between-toast-and-retry deletion is
        // caught by both the executor's pre-executeCommand check AND the
        // retry's re-check.
        assertHostLive: (h) => useHostStore.getState().hosts[h] != null,
      })
    } finally {
      executingRef.current = false
      setExecuting(false)
    }
  }, [hostId])

  const handleDelete = async (code: string) => {
    try {
      await hostFetch(hostId, `/api/sessions/${code}`, { method: 'DELETE' })
    } catch { /* will be removed by next WS sync */ }
    setDeletingCode(null)
  }

  return (
    <div className="max-w-2xl">
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-lg font-semibold">{t('hosts.sessions')}</h2>
        <div className="flex items-center gap-2">
          <CommandSlot
            mountTo={QUICK_COMMAND_SLOTS.HOST_ACTIONS}
            ctx={{ hostId }}
            // Finding 5 — same disable semantics as new-session button + Finding 3 ref guard
            busy={isOffline || executing}
            executor={runHostExecutor}
          />
          <button
            onClick={() => setShowNew(true)}
            disabled={isOffline}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50"
          >
            <Plus size={14} />
            {t('hosts.new_session')}
          </button>
        </div>
      </div>

      {showNew && <NewSessionDialog hostId={hostId} onClose={() => setShowNew(false)} />}

      {(() => {
        const errorMsg = isOffline ? connectionErrorMessage(runtime, t) : null
        return errorMsg && (
          <div className="text-xs text-red-400 px-3 py-2 mb-2">
            {errorMsg}
          </div>
        )
      })()}

      {sessions.length === 0 ? (
        <p className="text-sm text-text-muted">{t('hosts.no_sessions')}</p>
      ) : (
        <div className="border border-border-subtle rounded-lg overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="bg-surface-tertiary text-text-secondary text-xs">
                <th className="text-left px-3 py-2">{t('hosts.session_name')}</th>
                <th className="text-left px-3 py-2">{t('hosts.mode')}</th>
                <th className="text-left px-3 py-2">{t('hosts.agent')}</th>
                <th className="text-left px-3 py-2">{t('hosts.cwd')}</th>
                <th className="text-right px-3 py-2"></th>
              </tr>
            </thead>
            <tbody>
              {sessions.map((session) => {
                const agent = agentStatuses[compositeKey(hostId, session.code)]
                return (
                  <tr key={session.code} className="border-t border-border-subtle hover:bg-surface-secondary/30">
                    <td className="px-3 py-2">
                      {renamingCode === session.code ? (
                        <InlineRename hostId={hostId} session={session} onDone={() => setRenamingCode(null)} />
                      ) : (
                        <span className="text-text-primary">{session.name}</span>
                      )}
                    </td>
                    <td className="px-3 py-2 text-text-muted">{session.mode}</td>
                    <td className="px-3 py-2">
                      {agent ? (
                        <span className={`text-xs px-1.5 py-0.5 rounded ${
                          agent === 'running' ? 'bg-green-500/20 text-green-400'
                            : agent === 'error' ? 'bg-red-500/20 text-red-400'
                            : 'bg-surface-tertiary text-text-muted'
                        }`}>
                          {agent}
                        </span>
                      ) : (
                        <span className="text-text-muted">—</span>
                      )}
                    </td>
                    <td className="px-3 py-2 text-text-muted font-mono text-xs truncate max-w-[200px]">{session.cwd}</td>
                    <td className="px-3 py-2 text-right">
                      <div className="flex items-center justify-end gap-1">
                        {/* Phase 1c — v1 QuickCommandMenu removed from rows. Quick commands
                            are now consolidated at the new-session entry via Task 1c.1b
                            (HOST_ACTIONS slot). v1 QuickCommandMenu remains in
                            PaneLayoutRenderer. */}
                        <button
                          onClick={() => handleOpen(session, session.mode)}
                          disabled={isOffline}
                          title={t('hosts.open')}
                          className="p-1 rounded hover:bg-surface-tertiary text-text-secondary hover:text-accent cursor-pointer disabled:opacity-50"
                        >
                          <Play size={14} />
                        </button>
                        <button
                          onClick={() => setRenamingCode(session.code)}
                          disabled={isOffline}
                          title={t('hosts.rename')}
                          className="p-1 rounded hover:bg-surface-tertiary text-text-secondary hover:text-text-primary cursor-pointer disabled:opacity-50"
                        >
                          <PencilSimple size={14} />
                        </button>
                        {deletingCode === session.code ? (
                          <span className="flex items-center gap-1 text-xs">
                            <button
                              onClick={() => handleDelete(session.code)}
                              className="text-red-400 cursor-pointer"
                            >
                              <Check size={14} />
                            </button>
                            <button
                              onClick={() => setDeletingCode(null)}
                              className="text-text-muted cursor-pointer"
                            >
                              <X size={14} />
                            </button>
                          </span>
                        ) : (
                          <button
                            onClick={() => setDeletingCode(session.code)}
                            disabled={isOffline}
                            title={t('hosts.delete_session')}
                            className="p-1 rounded hover:bg-surface-tertiary text-text-secondary hover:text-red-400 cursor-pointer disabled:opacity-50"
                          >
                            <Trash size={14} />
                          </button>
                        )}
                      </div>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
