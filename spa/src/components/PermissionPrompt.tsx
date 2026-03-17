// spa/src/components/PermissionPrompt.tsx
import { WarningCircle } from '@phosphor-icons/react'

interface Props {
  tool: string
  description: string
  onAllow: () => void
  onDeny: () => void
}

export default function PermissionPrompt({ tool, description, onAllow, onDeny }: Props) {
  return (
    <div className="flex items-center gap-3 rounded-lg border border-yellow-800/60 bg-yellow-950/30 px-3 py-2.5 my-1">
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-1.5">
          <WarningCircle
            size={16}
            weight="fill"
            className="text-yellow-400 flex-shrink-0"
            data-testid="warning-icon"
          />
          <span className="text-sm font-medium text-yellow-300">{tool}</span>
        </div>
        <p className="text-xs text-gray-400 font-mono truncate mt-0.5">{description}</p>
      </div>
      <div className="flex gap-2 flex-shrink-0">
        <button
          onClick={onAllow}
          className="px-3 py-1 rounded-md text-xs font-medium bg-green-900 text-green-400 hover:bg-green-800 cursor-pointer"
        >
          Allow
        </button>
        <button
          onClick={onDeny}
          className="px-3 py-1 rounded-md text-xs font-medium bg-red-900 text-red-400 hover:bg-red-800 cursor-pointer"
        >
          Deny
        </button>
      </div>
    </div>
  )
}
