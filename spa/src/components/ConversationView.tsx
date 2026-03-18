// spa/src/components/ConversationView.tsx
import { useRef, useCallback, useState, useEffect } from 'react'
import { useStreamStore } from '../stores/useStreamStore'
import {
  type StreamMessage,
  type AssistantMessage,
  type UserMessage,
  type ControlRequest,
} from '../lib/stream-ws'
import MessageBubble from './MessageBubble'
import ToolCallBlock from './ToolCallBlock'
import ThinkingBlock from './ThinkingBlock'
import ToolResultBlock from './ToolResultBlock'
import PermissionPrompt from './PermissionPrompt'
import AskUserQuestion from './AskUserQuestion'
import StreamInput from './StreamInput'
import ThinkingIndicator from './ThinkingIndicator'
import FileAttachment, { type AttachedFile } from './FileAttachment'
import HandoffButton from './HandoffButton'
import { Prohibit, TerminalWindow } from '@phosphor-icons/react'

interface Props {
  sessionName: string
  onHandoff?: () => void
  onHandoffToTerm?: () => void
}

const EMPTY_MESSAGES: StreamMessage[] = []
const EMPTY_CONTROLS: ControlRequest[] = []

export default function ConversationView({ sessionName, onHandoff, onHandoffToTerm }: Props) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [attachedFiles, setAttachedFiles] = useState<AttachedFile[]>([])
  const [isDragging, setIsDragging] = useState(false)
  const dragCounter = useRef(0)

  // Read per-session state from store
  const messages = useStreamStore((s) => s.sessions[sessionName]?.messages ?? EMPTY_MESSAGES)
  const pendingControlRequests = useStreamStore((s) => s.sessions[sessionName]?.pendingControlRequests ?? EMPTY_CONTROLS)
  const isStreaming = useStreamStore((s) => s.sessions[sessionName]?.isStreaming ?? false)
  const conn = useStreamStore((s) => s.sessions[sessionName]?.conn ?? null)
  const handoffState = useStreamStore((s) => s.handoffState[sessionName] ?? 'idle')
  const handoffProgress = useStreamStore((s) => s.handoffProgress[sessionName] ?? '')
  const sessionStatus = useStreamStore((s) => s.sessionStatus[sessionName])

  // ThinkingIndicator: visible when streaming and no assistant messages yet
  const hasAssistantMessage = messages.some((m) => m.type === 'assistant')
  const showThinking = isStreaming && !hasAssistantMessage

  // Auto-scroll on new messages or control requests
  useEffect(() => {
    if (scrollRef.current?.scrollTo) {
      scrollRef.current.scrollTo({ top: scrollRef.current.scrollHeight, behavior: 'smooth' })
    }
  }, [messages, pendingControlRequests])

  const handleSend = useCallback((text: string) => {
    conn?.send({
      type: 'user',
      message: { role: 'user', content: text },
    })
    useStreamStore.getState().addMessage(sessionName, {
      type: 'user' as const,
      message: {
        role: 'user',
        content: [{ type: 'text', text }],
        stop_reason: null,
      },
    } as StreamMessage)
    useStreamStore.getState().setStreaming(sessionName, true)
    setAttachedFiles([])
  }, [conn, sessionName])

  const handleAllow = useCallback((req: ControlRequest) => {
    conn?.sendControlResponse(req.request_id, {
      behavior: 'allow',
      updatedInput: req.request.input,
    })
    useStreamStore.getState().resolveControlRequest(sessionName, req.request_id)
  }, [conn, sessionName])

  const handleDeny = useCallback((req: ControlRequest) => {
    conn?.sendControlResponse(req.request_id, {
      behavior: 'deny',
      message: 'User denied',
    })
    useStreamStore.getState().resolveControlRequest(sessionName, req.request_id)
  }, [conn, sessionName])

  const handleAskAnswer = useCallback((req: ControlRequest, answer: string) => {
    const input = req.request.input as Record<string, unknown> | undefined
    const questions = (input?.questions as Array<Record<string, unknown>>) || []
    const questionText = questions.length > 0
      ? (questions[0].question as string) || ''
      : ''
    conn?.sendControlResponse(req.request_id, {
      behavior: 'allow',
      updatedInput: {
        questions,
        answers: { [questionText]: answer },
      },
    })
    useStreamStore.getState().resolveControlRequest(sessionName, req.request_id)
  }, [conn, sessionName])

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
      useStreamStore.getState().setHandoffState(sessionName, 'handoff-in-progress')
    }
  }, [onHandoff, sessionName])

  // When handoffState is not 'connected', show the HandoffButton overlay
  if (handoffState !== 'connected') {
    return (
      <div className="flex flex-col h-full">
        <HandoffButton
          state={handoffState}
          progress={handoffProgress}
          sessionStatus={sessionStatus}
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
          const key = `${sessionName}-${i}`

          {/* --- Assistant messages --- */}
          if (msg.type === 'assistant' && 'message' in msg) {
            const am = msg as AssistantMessage
            return (
              <div key={key}>
                {am.message.content.map((block, j) => {
                  if (block.type === 'thinking' && block.thinking) {
                    return <ThinkingBlock key={j} content={block.thinking} />
                  }
                  if (block.type === 'text' && block.text) {
                    return <MessageBubble key={j} role="assistant" content={block.text} />
                  }
                  if (block.type === 'tool_use' && block.name) {
                    return <ToolCallBlock key={j} tool={block.name} input={block.input || {}} />
                  }
                  return null
                })}
              </div>
            )
          }

          {/* --- User messages --- */}
          if (msg.type === 'user' && 'message' in msg) {
            const um = msg as UserMessage
            const blocks = um.message.content

            return (
              <div key={key}>
                {blocks.map((block, j) => {
                  {/* Tool result */}
                  if (block.type === 'tool_result') {
                    const content = typeof block.content === 'string' ? block.content : JSON.stringify(block.content)
                    return <ToolResultBlock key={j} content={content} isError={block.is_error ?? false} />
                  }

                  {/* Text blocks */}
                  if (block.type === 'text' && block.text) {
                    {/* Interrupted */}
                    if (block.text === '[Request interrupted by user]') {
                      return (
                        <div key={j} data-testid="interrupted-msg"
                          className="flex items-center gap-1.5 bg-[#4a3038] rounded-[12px_12px_4px_12px] px-3 py-1.5 text-sm text-[#eaa] italic">
                          <Prohibit size={14} />
                          <span>Request interrupted by user</span>
                        </div>
                      )
                    }

                    {/* Slash command */}
                    if (block.text.startsWith('/')) {
                      return (
                        <div key={j} className="flex justify-end">
                          <div data-testid="command-bubble"
                            className="flex items-center gap-1.5 bg-[#4a4028] rounded-[12px_12px_4px_12px] px-3 py-1.5 text-[13px] text-[#e0d0a0] italic font-mono">
                            <TerminalWindow size={14} weight="bold" className="text-[#c0a060]" />
                            <span>{block.text}</span>
                          </div>
                        </div>
                      )
                    }

                    {/* Normal user text */}
                    return <MessageBubble key={j} role="user" content={block.text} />
                  }

                  return null
                })}
              </div>
            )
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
      <StreamInput onSend={handleSend} onAttach={handleAttach} onHandoffToTerm={onHandoffToTerm} disabled={isStreaming} />
    </div>
  )
}
