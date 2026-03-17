// spa/src/components/HandoffButton.tsx
import type { HandoffState } from '../stores/useStreamStore'

interface Props {
  presetName: string
  state: HandoffState
  progress?: string
  onHandoff: () => void
}

function progressLabel(progress: string): string {
  switch (progress) {
    case 'detecting': return 'Detecting CC...'
    case 'stopping-cc': return 'Stopping CC...'
    case 'launching': return 'Launching relay...'
    default: return progress || 'Connecting...'
  }
}

export default function HandoffButton({ presetName, state, progress = '', onHandoff }: Props) {
  if (state === 'connected') return null

  return (
    <div className="flex flex-col items-center justify-center h-full gap-3">
      <button
        onClick={onHandoff}
        disabled={state === 'handoff-in-progress'}
        className="px-4 py-2 rounded-lg bg-blue-600 text-white text-sm hover:bg-blue-500 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
      >
        {state === 'handoff-in-progress' ? progressLabel(progress) : `Start ${presetName}`}
      </button>
      {state === 'disconnected' && (
        <p className="text-xs text-gray-500">Session disconnected. Click to reconnect.</p>
      )}
    </div>
  )
}
