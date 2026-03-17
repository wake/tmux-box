// spa/src/components/SessionStatusBadge.tsx

export type SessionStatus = 'normal' | 'not-in-cc' | 'cc-idle' | 'cc-running' | 'cc-waiting' | 'cc-unread'

const STATUS_COLORS: Record<SessionStatus, string> = {
  'normal': 'bg-gray-500',
  'not-in-cc': 'bg-gray-500',
  'cc-idle': 'bg-emerald-700',
  'cc-running': 'bg-green-400',
  'cc-waiting': 'bg-yellow-400',
  'cc-unread': 'bg-blue-400',
}

interface Props {
  status: SessionStatus
}

export default function SessionStatusBadge({ status }: Props) {
  return (
    <span data-testid="status-badge"
      className={`inline-block w-2 h-2 rounded-full ${STATUS_COLORS[status] || 'bg-gray-500'}`}
      title={status}
    />
  )
}
