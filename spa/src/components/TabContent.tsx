import TerminalView from './TerminalView'
import ConversationView from './ConversationView'
import type { Tab } from '../types/tab'

interface Props {
  activeTab: Tab | null
  wsBase: string
  terminalKey?: number
  connectingMessage?: string
  onHandoff?: () => void
  onHandoffToTerm?: () => void
}

export function TabContent({
  activeTab, wsBase,
  terminalKey, connectingMessage,
  onHandoff, onHandoffToTerm,
}: Props) {
  if (!activeTab) {
    return (
      <div className="flex-1 flex items-center justify-center text-gray-600 text-sm">
        選擇或建立一個分頁開始使用
      </div>
    )
  }

  return (
    <div className="flex-1 overflow-hidden relative flex">
      {activeTab.type === 'terminal' && activeTab.sessionName && (
        <TerminalView
          key={`${activeTab.id}-${terminalKey}`}
          wsUrl={`${wsBase}/ws/terminal/${activeTab.sessionName}`}
          visible={true}
          connectingMessage={connectingMessage}
        />
      )}
      {activeTab.type === 'stream' && activeTab.sessionName && (
        <ConversationView
          sessionName={activeTab.sessionName}
          onHandoff={onHandoff}
          onHandoffToTerm={onHandoffToTerm}
        />
      )}
      {activeTab.type === 'editor' && (
        <div className="flex-1 flex items-center justify-center text-gray-500 text-sm">
          Editor: {activeTab.filePath ?? activeTab.label}（Phase 5 實作）
        </div>
      )}
    </div>
  )
}
