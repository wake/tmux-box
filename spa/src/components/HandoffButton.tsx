// spa/src/components/HandoffButton.tsx
import { Terminal } from '@phosphor-icons/react'
import type { SessionStatus } from './SessionStatusBadge'

interface Props {
  inProgress: boolean
  progress?: string
  sessionStatus?: SessionStatus
  onHandoff: () => void
}

function progressLabel(progress: string): string {
  switch (progress) {
    case 'starting': return 'Starting...'
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

export default function HandoffButton({ inProgress, progress = '', sessionStatus, onHandoff }: Props) {
  const ccAvailable = isCCRunning(sessionStatus)
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
      {!ccAvailable && !inProgress && (
        <p className="text-xs text-gray-500">No CC running</p>
      )}
    </div>
  )
}
