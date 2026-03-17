// spa/src/components/ThinkingIndicator.tsx

interface Props {
  visible: boolean
}

export default function ThinkingIndicator({ visible }: Props) {
  if (!visible) return null

  return (
    <div data-testid="thinking-indicator" className="flex justify-start">
      <div className="bg-[#2a2f38] rounded-xl px-4 py-3 flex items-center gap-1.5">
        {[0, 1, 2].map(i => (
          <span
            key={i}
            className="w-1.5 h-1.5 rounded-full bg-blue-400 animate-pulse"
            style={{ animationDelay: `${i * 200}ms` }}
          />
        ))}
      </div>
    </div>
  )
}
