// spa/src/lib/register-builtins.tsx
import { registerTabRenderer } from './tab-registry'
import { SessionTabContent } from '../components/SessionTabContent'

export function registerBuiltinRenderers(): void {
  registerTabRenderer('session', {
    component: SessionTabContent,
    viewModes: ['terminal', 'stream'],
    defaultViewMode: 'terminal',
    icon: (tab) => tab.viewMode === 'stream' ? 'ChatCircleDots' : 'TerminalWindow',
  })

  registerTabRenderer('editor', {
    component: ({ tab }) => (
      <div className="flex-1 flex items-center justify-center text-gray-500 text-sm">
        Editor: {(tab.data.filePath as string) ?? tab.label}（Phase 4 實作）
      </div>
    ),
    icon: () => 'File',
  })
}
