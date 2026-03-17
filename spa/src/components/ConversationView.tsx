// spa/src/components/ConversationView.tsx
import { useEffect, useRef, useCallback, useState } from 'react'
import { useStreamStore } from '../stores/useStreamStore'
import {
  connectStream,
  type StreamMessage,
  type AssistantMessage,
  type UserMessage,
  type SystemMessage,
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
  presetName: string
  onHandoff?: () => void
}

export default function ConversationView({ wsUrl, presetName, onHandoff }: Props) {
  const connRef = useRef<ReturnType<typeof connectStream> | null>(null)
  const scrollRef = useRef<HTMLDivElement>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [attachedFiles, setAttachedFiles] = useState<AttachedFile[]>([])
  const [isDragging, setIsDragging] = useState(false)
  const dragCounter = useRef(0)
  const {
    messages,
    pendingControlRequests,
    isStreaming,
    handoffState,
    handoffProgress,
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
        if (msg.type === 'system') {
          const sys = msg as SystemMessage
          if (sys.subtype === 'init') {
            setSessionInfo(sys.session_id ?? '', sys.model ?? '')
            setHandoffState('connected')
          }
          return
        }
        if (msg.type === 'control_request') {
          addControlRequest(msg as ControlRequest)
          return
        }
        if (msg.type === 'result' && 'total_cost_usd' in msg) {
          addCost((msg as { total_cost_usd?: number }).total_cost_usd || 0)
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

  // File attachment helpers
  function processFiles(files: FileList | File[]) {
    Array.from(files).forEach((file) => {
      if (file.type.startsWith('image/')) {
        const reader = new FileReader()
        reader.onload = (ev) => {
          setAttachedFiles((prev) => [...prev, {
            name: file.name,
            type: file.type,
            url: (ev.target?.result as string) || '',
          }])
        }
        reader.readAsDataURL(file)
      } else {
        setAttachedFiles((prev) => [...prev, { name: file.name, type: file.type, url: '' }])
      }
    })
  }

  function handleAttach() {
    fileInputRef.current?.click()
  }

  function handleFileChange(e: React.ChangeEvent<HTMLInputElement>) {
    if (e.target.files) {
      processFiles(e.target.files)
    }
    e.target.value = '' // reset for re-select
  }

  // Drag-drop handlers
  function handleDragEnter(e: React.DragEvent) {
    e.preventDefault()
    dragCounter.current++
    setIsDragging(true)
  }

  function handleDragOver(e: React.DragEvent) {
    e.preventDefault()
  }

  function handleDragLeave(e: React.DragEvent) {
    e.preventDefault()
    dragCounter.current--
    if (dragCounter.current <= 0) {
      setIsDragging(false)
      dragCounter.current = 0
    }
  }

  function handleDrop(e: React.DragEvent) {
    e.preventDefault()
    setIsDragging(false)
    dragCounter.current = 0
    if (e.dataTransfer.files.length > 0) {
      processFiles(e.dataTransfer.files)
    }
  }

  const handleHandoff = useCallback(() => {
    if (onHandoff) {
      onHandoff()
    } else {
      setHandoffState('handoff-in-progress')
    }
  }, [onHandoff, setHandoffState])

  // When handoffState is not 'connected', show the HandoffButton overlay
  if (handoffState !== 'connected') {
    return (
      <div className="flex flex-col h-full">
        <HandoffButton
          presetName={presetName || 'session'}
          state={handoffState}
          progress={handoffProgress}
          onHandoff={handleHandoff}
        />
      </div>
    )
  }

  return (
    <div
      className="flex flex-col h-full relative"
      onDragEnter={handleDragEnter}
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
    >
      {/* Drag overlay */}
      {isDragging && (
        <div className="absolute inset-0 bg-blue-500/10 border-2 border-dashed border-blue-400 z-20 flex items-center justify-center pointer-events-none">
          <span className="text-blue-400 font-medium">Drop files here</span>
        </div>
      )}

      {/* Hidden file input */}
      <input
        ref={fileInputRef}
        type="file"
        multiple
        className="hidden"
        onChange={handleFileChange}
      />

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
            const am = msg as AssistantMessage
            const content = am.message.content
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
            const um = msg as UserMessage
            const textBlock = um.message.content.find(
              (b) => b.type === 'text',
            )
            if (textBlock && textBlock.text) {
              return <MessageBubble key={key} role="user" content={textBlock.text} />
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
      <StreamInput onSend={handleSend} onAttach={handleAttach} disabled={isStreaming} />
    </div>
  )
}
