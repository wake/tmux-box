import { SmileySad } from '@phosphor-icons/react'
import { useTabStore } from '../stores/useTabStore'
import { useI18nStore } from '../stores/useI18nStore'
import { closeTab } from '../lib/tab-lifecycle'
import { SessionPickerList, type SessionSelection } from './SessionPickerList'
import type { PaneContent, TerminatedReason } from '../types/tab'

interface Props {
  content: Extract<PaneContent, { kind: 'tmux-session' }>
  tabId: string
  paneId: string
}

const REASON_KEYS: Record<TerminatedReason, { title: string; desc: string }> = {
  'session-closed': { title: 'terminated.session_closed', desc: 'terminated.session_closed_desc' },
  'tmux-restarted': { title: 'terminated.tmux_restarted', desc: 'terminated.tmux_restarted_desc' },
  'host-removed': { title: 'terminated.host_removed', desc: 'terminated.host_removed_desc' },
}

export function TerminatedPane({ content, tabId, paneId }: Props) {
  const t = useI18nStore((s) => s.t)
  const setPaneContent = useTabStore((s) => s.setPaneContent)
  const reason = content.terminated!
  const keys = REASON_KEYS[reason]

  // Re-pointing drops `terminated` and takes the generation the picker read off
  // the selected session's own payload (spec §4.5) — so the freshly attached
  // pane is not immediately marked dead by the next reconciliation.
  const handleSelect = (sel: SessionSelection) => {
    setPaneContent(tabId, paneId, {
      kind: 'tmux-session',
      hostId: sel.hostId,
      sessionCode: sel.sessionCode,
      mode: content.mode,
      cachedName: sel.cachedName,
      tmuxInstance: sel.tmuxInstance,
    })
  }

  return (
    <div className="flex flex-col items-center justify-center h-full p-8 text-center">
      <SmileySad size={48} className="text-zinc-500 mb-4" />
      <h2 className="text-lg font-medium text-zinc-300 mb-1">{t(keys.title)}</h2>
      <p className="text-sm text-zinc-500 mb-6">{t(keys.desc, { name: content.cachedName })}</p>
      <button className="text-sm text-zinc-400 hover:text-zinc-200 mb-8" onClick={() => {
        closeTab(tabId)
      }}>
        {t('terminated.close_tab')}
      </button>
      <div className="w-full max-w-sm">
        <SessionPickerList onSelect={handleSelect} />
      </div>
    </div>
  )
}
