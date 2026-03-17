// spa/src/components/ConversationView.tsx
import { useEffect, useRef, useCallback, useState } from 'react'
import { useStreamStore } from '../stores/useStreamStore'
import {
  connectStream,
  type StreamMessage,
  type ControlRequest,
} from '../lib/stream-ws'
import MessageBubble from './MessageBubble'
import ToolCallBlock from './ToolCallBlock'
import PermissionPrompt from './PermissionPrompt'
import AskUserQuestion from './AskUserQuestion'
import StreamInput from './StreamInput'
import ThinkingIndicator from './ThinkingIndicator'
import FileAttachment, { type AttachedFile } from './FileAttachment'
import HandoffButton from './HandoffButton'

interface Props {
  wsUrl: string
  sessionName: string
  presetName?: string
}

export default function ConversationView({ wsUrl, presetName }: Props) {
  const connRef = useRef<ReturnType<typeof connectStream> | null>(null)
  const scrollRef = useRef<HTMLDivElement>(null)
  const [attachedFiles, setAttachedFiles] = useState<AttachedFile[]>([])
  const {
    messages,
    pendingControlRequests,
    isStreaming,
    handoffState,
    addMessage,
    addControlRequest,
    resolveControlRequest,
    setStreaming,
    setSessionInfo,
    addCost,
    setConn,
    setHandoffState,
    clear,
  } = useStreamStore()

  // ThinkingIndicator: visible when streaming and no assistant messages yet
  const hasAssistantMessage = messages.some((m) => m.type === 'assistant')
  const showThinking = isStreaming && !hasAssistantMessage

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
          setHandoffState('connected')
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
      () => {
        setStreaming(false)
        setHandoffState('disconnected')
      },
      // onOpen: intentionally no-op — isStreaming is set in handleSend
    )
    connRef.current = conn
    setConn(conn)

    return () => {
      conn.close()
      connRef.current = null
      setConn(null)
    }
  }, [wsUrl]) // eslint-disable-line react-hooks/exhaustive-deps

  // Auto-scroll on new messages or control requests
  useEffect(() => {
    if (scrollRef.current?.scrollTo) {
      scrollRef.current.scrollTo({ top: scrollRef.current.scrollHeight, behavior: 'smooth' })
    }
  }, [messages, pendingControlRequests])

  const handleSend = useCallback((text: string) => {
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
    // Clear attached files after send
    setAttachedFiles([])
  }, [addMessage, setStreaming])

  const handleAllow = useCallback((req: ControlRequest) => {
    connRef.current?.sendControlResponse(req.request_id, {
      behavior: 'allow',
      updatedInput: req.request.input,
    })
    resolveControlRequest(req.request_id)
  }, [resolveControlRequest])

  const handleDeny = useCallback((req: ControlRequest) => {
    connRef.current?.sendControlResponse(req.request_id, {
      behavior: 'deny',
      message: 'User denied',
    })
    resolveControlRequest(req.request_id)
  }, [resolveControlRequest])

  const handleAskAnswer = useCallback((req: ControlRequest, answer: string) => {
    const input = req.request.input as Record<string, unknown> | undefined
    const questions = (input?.questions as Array<Record<string, unknown>>) || []
    const questionText = questions.length > 0
      ? (questions[0].question as string) || ''
      : ''
    connRef.current?.sendControlResponse(req.request_id, {
      behavior: 'allow',
      updatedInput: {
        questions,
        answers: { [questionText]: answer },
      },
    })
    resolveControlRequest(req.request_id)
  }, [resolveControlRequest])

  const handleRemoveFile = useCallback((index: number) => {
    setAttachedFiles((prev) => prev.filter((_, i) => i !== index))
  }, [])

  const handleHandoff = useCallback(() => {
    // Handoff is triggered by the caller (App.tsx) via store or props.
    // For now this is a placeholder — the actual handoff API call
    // will be wired in the App-level integration task.
    setHandoffState('handoff-in-progress')
  }, [setHandoffState])

  // When handoffState is not 'connected', show the HandoffButton overlay
  if (handoffState !== 'connected') {
    return (
      <div className="flex flex-col h-full">
        <HandoffButton
          presetName={presetName || 'session'}
          state={handoffState}
          onHandoff={handleHandoff}
        />
      </div>
    )
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
          const key = `${wsUrl}-${i}`
          if (msg.type === 'assistant' && 'message' in msg) {
            const content = msg.message.content
            return (
              <div key={key}>
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
              return <MessageBubble key={key} role="user" content={textBlock.text as string} />
            }
          }
          return null
        })}

        {/* Thinking indicator */}
        <ThinkingIndicator visible={showThinking} />

        {/* Pending control requests */}
        {pendingControlRequests.map((req) => {
          if (req.request.tool_name === 'AskUserQuestion') {
            const input = req.request.input as Record<string, unknown> | undefined
            const questions = (input?.questions as Array<{
              question: string
              header?: string
              options?: Array<{ label: string; description?: string }>
              multiSelect?: boolean
            }>) || []
            return (
              <AskUserQuestion
                key={req.request_id}
                questions={questions}
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

      {/* File attachments */}
      <FileAttachment files={attachedFiles} onRemove={handleRemoveFile} />

      {/* Input area */}
      <StreamInput onSend={handleSend} disabled={isStreaming} />
    </div>
  )
}
