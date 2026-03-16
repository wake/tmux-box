// spa/src/components/ConversationView.tsx
import { useEffect, useRef } from 'react'
import { useStreamStore } from '../stores/useStreamStore'
import {
  connectStream,
  type StreamConnection,
  type StreamMessage,
  type ControlRequest,
} from '../lib/stream-ws'
import MessageBubble from './MessageBubble'
import ToolCallBlock from './ToolCallBlock'
import PermissionPrompt from './PermissionPrompt'
import AskUserQuestion from './AskUserQuestion'
import StreamInput from './StreamInput'

interface Props {
  wsUrl: string
  sessionName: string
}

export default function ConversationView({ wsUrl }: Props) {
  const connRef = useRef<StreamConnection | null>(null)
  const scrollRef = useRef<HTMLDivElement>(null)
  const {
    messages,
    pendingControlRequests,
    isStreaming,
    addMessage,
    addControlRequest,
    resolveControlRequest,
    setStreaming,
    setSessionInfo,
    addCost,
    clear,
  } = useStreamStore()

  useEffect(() => {
    clear()

    const conn = connectStream(
      wsUrl,
      (msg) => {
        if (msg.type === 'system' && 'subtype' in msg && msg.subtype === 'init') {
          setSessionInfo(
            (msg as Record<string, unknown>).session_id as string,
            (msg as Record<string, unknown>).model as string,
          )
          return
        }
        if (msg.type === 'control_request') {
          addControlRequest(msg as ControlRequest)
          return
        }
        if (msg.type === 'result' && 'total_cost_usd' in msg) {
          addCost(((msg as Record<string, unknown>).total_cost_usd as number) || 0)
          setStreaming(false)
          return
        }
        if (msg.type === 'assistant' || msg.type === 'user') {
          addMessage(msg)
        }
      },
      () => setStreaming(false),
      () => setStreaming(true),
    )
    connRef.current = conn
    ;(window as any).__streamConn = conn

    return () => {
      conn.close()
      connRef.current = null
      ;(window as any).__streamConn = null
    }
  }, [wsUrl]) // eslint-disable-line react-hooks/exhaustive-deps

  // Auto-scroll on new messages or control requests
  useEffect(() => {
    if (scrollRef.current?.scrollTo) {
      scrollRef.current.scrollTo({ top: scrollRef.current.scrollHeight, behavior: 'smooth' })
    }
  }, [messages, pendingControlRequests])

  const handleSend = (text: string) => {
    connRef.current?.send({
      type: 'user',
      message: { role: 'user', content: text },
    })
    // Add user message locally for immediate display
    addMessage({
      type: 'user' as const,
      message: {
        role: 'user',
        content: [{ type: 'text', text }],
        stop_reason: null,
      },
    } as StreamMessage)
    setStreaming(true)
  }

  const handleAllow = (req: ControlRequest) => {
    connRef.current?.sendControlResponse(req.request_id, {
      behavior: 'allow',
      updatedInput: req.request.input,
    })
    resolveControlRequest(req.request_id)
  }

  const handleDeny = (req: ControlRequest) => {
    connRef.current?.sendControlResponse(req.request_id, {
      behavior: 'deny',
      message: 'User denied',
    })
    resolveControlRequest(req.request_id)
  }

  const handleAskAnswer = (req: ControlRequest, answer: string) => {
    connRef.current?.sendControlResponse(req.request_id, {
      behavior: 'allow',
      updatedInput: { answer },
    })
    resolveControlRequest(req.request_id)
  }

  return (
    <div className="flex flex-col h-full">
      {/* Messages area */}
      <div ref={scrollRef} className="flex-1 overflow-y-auto p-4 space-y-4">
        {messages.length === 0 && !isStreaming && (
          <div className="flex items-center justify-center h-full text-gray-500 text-sm">
            Waiting for messages...
          </div>
        )}
        {messages.map((msg, i) => {
          if (msg.type === 'assistant' && 'message' in msg) {
            const content = msg.message.content
            return (
              <div key={i}>
                {content.map((block, j) => {
                  if (block.type === 'text' && block.text) {
                    return <MessageBubble key={j} role="assistant" content={block.text} />
                  }
                  if (block.type === 'tool_use' && block.name) {
                    return (
                      <ToolCallBlock key={j} tool={block.name} input={block.input || {}} />
                    )
                  }
                  return null
                })}
              </div>
            )
          }
          if (msg.type === 'user' && 'message' in msg) {
            const textBlock = msg.message.content.find(
              (b: { type: string }) => b.type === 'text',
            )
            if (textBlock && 'text' in textBlock) {
              return <MessageBubble key={i} role="user" content={textBlock.text as string} />
            }
          }
          return null
        })}

        {/* Pending control requests */}
        {pendingControlRequests.map((req) => {
          if (req.request.tool_name === 'AskUserQuestion') {
            const input = req.request.input as Record<string, unknown> | undefined
            const question = (input?.question as string) || 'Please answer:'
            const options = (input?.options as string[]) || []
            const multiSelect = (input?.multiSelect as boolean) || false
            return (
              <AskUserQuestion
                key={req.request_id}
                question={question}
                options={options}
                multiSelect={multiSelect}
                onSubmit={(answer) => handleAskAnswer(req, answer)}
                onCancel={() => handleDeny(req)}
              />
            )
          }
          const toolName = req.request.tool_name || 'Unknown'
          const description = req.request.input
            ? JSON.stringify(req.request.input).slice(0, 200)
            : 'Permission requested'
          return (
            <PermissionPrompt
              key={req.request_id}
              tool={toolName}
              description={description}
              onAllow={() => handleAllow(req)}
              onDeny={() => handleDeny(req)}
            />
          )
        })}
      </div>

      {/* Input area */}
      <StreamInput onSend={handleSend} disabled={isStreaming} />
    </div>
  )
}
