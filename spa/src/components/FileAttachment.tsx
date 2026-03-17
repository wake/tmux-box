// spa/src/components/FileAttachment.tsx
import { File, X } from '@phosphor-icons/react'

export interface AttachedFile {
  name: string
  type: string
  url: string // base64 data URL for images
}

interface Props {
  files: AttachedFile[]
  onRemove: (index: number) => void
}

export default function FileAttachment({ files, onRemove }: Props) {
  if (files.length === 0) return null

  return (
    <div className="flex gap-2 px-3 py-1.5 flex-wrap" data-testid="file-attachments">
      {files.map((file, i) => (
        <div
          key={i}
          className="flex items-center gap-1.5 bg-[#2a2f38] border border-[#484f5a] rounded-lg px-2 py-1 text-xs text-[#dde0e5]"
        >
          {file.type.startsWith('image/') && file.url ? (
            <div
              className="w-8 h-8 rounded bg-cover bg-center"
              style={{ backgroundImage: `url(${file.url})` }}
            />
          ) : (
            <File size={14} className="text-gray-400" data-testid="file-icon" />
          )}
          <span className="max-w-[100px] truncate">{file.name}</span>
          <button
            aria-label="remove"
            onClick={() => onRemove(i)}
            className="text-gray-500 hover:text-red-400 ml-0.5 cursor-pointer"
          >
            <X size={12} />
          </button>
        </div>
      ))}
    </div>
  )
}
