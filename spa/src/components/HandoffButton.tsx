// spa/src/components/HandoffButton.tsx
import { Terminal } from '@phosphor-icons/react'
import type { HandoffState } from '../stores/useStreamStore'
import type { SessionStatus } from './SessionStatusBadge'

interface Props {
  state: HandoffState
  progress?: string
  sessionStatus?: SessionStatus
  onHandoff: () => void
}

function progressLabel(progress: string): string {
  switch (progress) {
    case 'detecting': return 'Detecting CC...'
    case 'stopping-cc': return 'Stopping CC...'
    case 'extracting-id': return 'Extracting session...'
    case 'exiting-cc': return 'Exiting CC...'
    case 'launching': return 'Launching relay...'
    case 'stopping-relay': return 'Stopping relay...'
    case 'waiting-shell': return 'Waiting for shell...'
    case 'launching-cc': return 'Launching CC...'
    default: return progress || 'Connecting...'
  }
}

function isCCRunning(status?: SessionStatus): boolean {
  return status === 'cc-idle' || status === 'cc-running' || status === 'cc-waiting'
}

export default function HandoffButton({ state, progress = '', sessionStatus, onHandoff }: Props) {
  if (state === 'connected') return null

  const ccAvailable = isCCRunning(sessionStatus)
  const inProgress = state === 'handoff-in-progress'
  const disabled = inProgress || !ccAvailable

  return (
    <div className="flex flex-col items-center justify-center h-full gap-3">
      <button
        onClick={onHandoff}
        disabled={disabled}
        className="flex items-center gap-2 px-4 py-2 rounded-lg bg-blue-600 text-white text-sm hover:bg-blue-500 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
      >
        <Terminal size={16} />
        {inProgress ? progressLabel(progress) : 'Handoff'}
      </button>
      {state === 'disconnected' && (
        <p className="text-xs text-gray-500">Session disconnected. Click to reconnect.</p>
      )}
      {!ccAvailable && !inProgress && (
        <p className="text-xs text-gray-500">No CC running</p>
      )}
    </div>
  )
}
