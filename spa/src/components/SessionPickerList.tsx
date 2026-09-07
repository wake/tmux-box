import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useSessionWatch } from '../hooks/useSessionWatch'

export interface SessionSelection {
  hostId: string
  sessionCode: string
  cachedName: string
  tmuxInstance: string
}

interface Props {
  onSelect: (selection: SessionSelection) => void
}

export function SessionPickerList({ onSelect }: Props) {
  useSessionWatch()
  const t = useI18nStore((s) => s.t)
  const hosts = useHostStore((s) => s.hosts)
  const hostOrder = useHostStore((s) => s.hostOrder)
  const runtime = useHostStore((s) => s.runtime)
  const sessions = useSessionStore((s) => s.sessions)

  const connectedHosts = hostOrder.filter((id) => runtime[id]?.status === 'connected')

  if (connectedHosts.length === 0) {
    return <div className="text-center text-zinc-500 py-8">{t('terminated.no_sessions')}</div>
  }

  return (
    <div className="space-y-4">
      <p className="text-xs text-zinc-400">{t('terminated.select_session')}</p>
      {connectedHosts.map((hostId) => {
        const host = hosts[hostId]
        const hostSessions = sessions[hostId] ?? []
        if (!host || hostSessions.length === 0) return null
        return (
          <div key={hostId}>
            <div className="text-xs text-zinc-500 mb-1">{host.name}</div>
            <div className="space-y-1">
              {hostSessions.map((s) => (
                <button
                  key={s.code}
                  className="w-full text-left px-3 py-2 rounded hover:bg-zinc-700/50 text-sm"
                  onClick={() =>
                    onSelect({
                      hostId,
                      sessionCode: s.code,
                      cachedName: s.name,
                      // From the session payload itself, never from ambient
                      // host state (spec §3.5).
                      tmuxInstance: s.tmux_instance ?? '',
                    })
                  }
                >
                  {s.name}
                </button>
              ))}
            </div>
          </div>
        )
      })}
    </div>
  )
}
