interface Props {
  hostName: string | null
  sessionName: string | null
  status: string | null
  mode: string | null
}

export function StatusBar({ hostName, sessionName, status, mode }: Props) {
  if (!sessionName) {
    return (
      <div className="h-6 bg-[#12122a] border-t border-gray-800 flex items-center px-3 text-[10px] text-gray-600 flex-shrink-0">
        No active session
      </div>
    )
  }

  return (
    <div className="h-6 bg-[#12122a] border-t border-gray-800 flex items-center px-3 text-[10px] text-gray-600 gap-3 flex-shrink-0">
      <span>{hostName}</span>
      <span>{sessionName}</span>
      <span className={status === 'connected' ? 'text-green-500' : 'text-gray-600'}>
        {status}
      </span>
      <span className="ml-auto">{mode}</span>
    </div>
  )
}
