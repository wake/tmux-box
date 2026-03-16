// spa/src/components/PermissionPrompt.tsx
import { ShieldWarning } from '@phosphor-icons/react'

interface Props {
  tool: string
  description: string
  onAllow: () => void
  onDeny: () => void
}

export default function PermissionPrompt({ tool, description, onAllow, onDeny }: Props) {
  return (
    <div className="rounded-xl border border-yellow-600/40 bg-yellow-950/30 px-4 py-3 mx-4 my-2">
      <div className="flex items-start gap-3">
        {/* Icon */}
        <ShieldWarning
          size={20}
          weight="fill"
          className="text-yellow-400 flex-shrink-0 mt-0.5"
          data-testid="shield-icon"
        />

        {/* Body */}
        <div className="flex-1 min-w-0">
          <p className="text-sm font-semibold text-yellow-200 mb-0.5">
            Permission required: <span className="font-mono">{tool}</span>
          </p>
          <p className="text-sm text-yellow-300/80">{description}</p>
        </div>
      </div>

      {/* Buttons */}
      <div className="flex gap-2 mt-3 justify-end">
        <button
          data-testid="deny-btn"
          onClick={onDeny}
          className="px-3 py-1.5 rounded-md text-sm font-medium bg-gray-800 text-gray-300 hover:bg-gray-700 cursor-pointer"
        >
          Deny
        </button>
        <button
          data-testid="allow-btn"
          onClick={onAllow}
          className="px-3 py-1.5 rounded-md text-sm font-medium bg-yellow-600 text-white hover:bg-yellow-500 cursor-pointer"
        >
          Allow
        </button>
      </div>
    </div>
  )
}
