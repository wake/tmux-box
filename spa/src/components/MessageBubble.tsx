// spa/src/components/MessageBubble.tsx
import ReactMarkdown from 'react-markdown'
import rehypeHighlight from 'rehype-highlight'
import { User, Robot } from '@phosphor-icons/react'
import 'highlight.js/styles/github-dark.css'

interface Props {
  role: 'user' | 'assistant'
  content: string
}

export default function MessageBubble({ role, content }: Props) {
  const isUser = role === 'user'

  return (
    <div className={`flex gap-3 px-4 py-3 ${isUser ? 'flex-row-reverse' : 'flex-row'}`}>
      {/* Icon */}
      <div className={`flex-shrink-0 w-7 h-7 rounded-full flex items-center justify-center ${isUser ? 'bg-blue-600' : 'bg-gray-700'}`}>
        {isUser ? (
          <User size={15} weight="bold" className="text-white" data-testid="icon-user" />
        ) : (
          <Robot size={15} weight="bold" className="text-gray-200" data-testid="icon-assistant" />
        )}
      </div>

      {/* Content */}
      <div className={`max-w-[75%] rounded-xl px-3 py-2 text-sm ${isUser ? 'bg-blue-600 text-white' : 'bg-gray-800 text-gray-100'}`}>
        {isUser ? (
          <p className="whitespace-pre-wrap break-words">{content}</p>
        ) : (
          <div className="prose prose-invert prose-sm max-w-none">
            <ReactMarkdown rehypePlugins={[rehypeHighlight]}>
              {content}
            </ReactMarkdown>
          </div>
        )}
      </div>
    </div>
  )
}
