// spa/src/components/MessageBubble.tsx
import ReactMarkdown from 'react-markdown'
import rehypeHighlight from 'rehype-highlight'
import 'highlight.js/styles/github-dark.css'

interface Props {
  role: 'user' | 'assistant'
  content: string
}

export default function MessageBubble({ role, content }: Props) {
  if (role === 'user') {
    return (
      <div className="flex justify-end">
        <div
          data-testid="user-bubble"
          className="max-w-[75%] bg-[#334a5e] text-[#dde8f5] text-sm rounded-[12px_12px_4px_12px] px-3 py-1.5 pl-2.5"
        >
          <p className="whitespace-pre-wrap break-words">{content}</p>
        </div>
      </div>
    )
  }

  return (
    <div data-testid="assistant-text" className="max-w-[90%] text-sm leading-[1.7] text-[#e0e0e0]">
      <div className="prose prose-invert prose-sm max-w-none">
        <ReactMarkdown rehypePlugins={[rehypeHighlight]}>
          {content}
        </ReactMarkdown>
      </div>
    </div>
  )
}
