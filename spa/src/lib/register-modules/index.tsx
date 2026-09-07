/* eslint-disable react-refresh/only-export-components */
import { useState } from 'react'
import { FolderOpen } from '@phosphor-icons/react'
import { getModules, registerModule } from '../module-registry'
import { registerNewTabProvider } from '../new-tab-registry'
import { registerSettingsSection } from '../settings-section-registry'
import {
  dispatchSettingsContributions,
  resetSettingsContributionsForHmr,
} from '../dispatch-settings-contributions'
import { findPane } from '../pane-tree'
import { getPlatformCapabilities } from '../platform'
import { SessionPaneContent } from '../../components/SessionPaneContent'
import { NewTabPage } from '../../components/NewTabPage'
import { DashboardPage } from '../../components/DashboardPage'
import { HistoryPage } from '../../components/HistoryPage'
import { SettingsPage } from '../../components/SettingsPage'
import { SessionSection } from '../../components/SessionSection'
import { BrowserPane } from '../../components/BrowserPane'
import { BrowserNewTabSection } from '../../components/BrowserNewTabSection'
import { MemoryMonitorPage } from '../../components/MemoryMonitorPage'
import { HostPage } from '../../components/HostPage'
import { ExecutionDetailPage } from '../../components/ExecutionDetailPage'
import { AppearanceSection } from '../../components/settings/AppearanceSection'
import { TerminalSection } from '../../components/settings/TerminalSection'
import { ElectronSection } from '../../components/settings/ElectronSection'
import { DevEnvironmentSection } from '../../components/settings/DevEnvironmentSection'
import { TmuxAgentMonitorSection } from '../../components/settings/TmuxAgentMonitorSection'
import { ModulesSwitchboardSection } from '../../components/settings/ModulesSwitchboardSection'
import { SyncSection } from '../../components/settings/SyncSection'
import { SnapshotSettingsSection } from '../../components/settings/SnapshotSettingsSection'
import { FileTreeWorkspaceView } from '../../components/FileTreeView'
import { FileTreeSessionView } from '../../components/FileTreeSessionView'
import { useTabStore } from '../../stores/useTabStore'
import type { PaneContent } from '../../types/tab'
import type { PaneRendererProps } from '../module-registry'
import { registerSyncContributors } from '../sync/register-sync'
import {
  registerInterfaceSubsection,
  getInterfaceSubsections,
} from '../interface-subsection-registry'
import { InterfaceSection } from '../../components/settings/InterfaceSection'
import { NewTabSubsection } from '../../components/settings/new-tab/NewTabSubsection'
import { registerBuiltinTerminalLinks, __resetBuiltinTerminalLinks } from '../terminal-link'
import { fetchSessionCwd, fetchSessionHome } from '../host-api'
import { useWorkspaceStore } from '../../stores/useWorkspaceStore'
import { openBrowserTab } from '../open-browser-tab'
import { setHostBuiltinSections } from '../host-builtin-sections'
import { useModuleEnabledStore } from '../../stores/useModuleEnabledStore'
import { OverviewSection } from '../../components/hosts/OverviewSection'
import { SessionsSection } from '../../components/hosts/SessionsSection'
import { HooksSection } from '../../components/hosts/HooksSection'
import { AgentsSection } from '../../components/hosts/AgentsSection'
import { UploadSection } from '../../components/hosts/UploadSection'
import { LogsSection } from '../../components/hosts/LogsSection'
import { editorModuleDefinition, registerEditorNewTabProviders } from './editor-module'
import { registerBuiltinFsBackends } from './fs-backends'
import {
  tryOpenFileForTerminalLink,
  openFileAsBufferDirect,
  resolveOpenContextCwdFromSessions,
} from './file-open-bootstrap'
import { applyModuleFileOpeners } from './module-file-openers'
import { clearAllForHmr as clearFileOpenerRegistryForHmr } from '../file-opener-registry'
import { QuickCommandsSettingsSection } from '../../components/settings/QuickCommandsSettingsSection'
import { FilesWorkspaceSettingsSection } from '../../components/settings/FilesWorkspaceSettingsSection'
import { PlaceholderSettingsSection } from '../../components/settings/PlaceholderSettingsSection'
import { SETTINGS_ORDER } from '../settings-order'

function NewTabPaneWrapper({ pane }: PaneRendererProps) {
  // Reverse-lookup the owning tab from the pane id. Subscribing to `tabs` keeps
  // `currentTabId` correct if the layout changes under us, and lets the
  // "Bring in an open tab" section (rendered inside NewTabPage) target this pane.
  const tabs = useTabStore((s) => s.tabs)
  const currentTabId = Object.keys(tabs).find((id) =>
    findPane(tabs[id].layout, pane.id) !== undefined,
  )
  const handleSelect = (content: PaneContent) => {
    if (!currentTabId) return
    useTabStore.getState().setPaneContent(currentTabId, pane.id, content)
    useTabStore.getState().setActiveTab(currentTabId)
  }
  return (
    <NewTabPage onSelect={handleSelect} currentTabId={currentTabId} currentPaneId={pane.id} />
  )
}

function BrowserPaneWrapper({ pane }: PaneRendererProps) {
  const content = pane.content
  if (content.kind !== 'browser') return null
  return <BrowserPane paneId={pane.id} url={content.url} />
}

function MemoryMonitorPaneWrapper() {
  return <MemoryMonitorPage />
}

function ExecutionPaneWrapper({ pane }: PaneRendererProps) {
  const content = pane.content
  if (content.kind !== 'execution') return null
  return <ExecutionDetailPage executionId={content.executionId} host={content.host} />
}

function PerformanceMonitorSettingsSection() {
  return <MemoryMonitorPage />
}

function InterfaceSectionHost() {
  const subs = getInterfaceSubsections()
  const [active, setActive] = useState<string>(() => subs[0]?.id ?? '')
  if (subs.length === 0) {
    // Defensive: registry not populated yet (e.g. HMR ordering race).
    // Avoid blank-area silent failure; next render should succeed.
    return <div className="flex-1 p-6 text-sm text-text-muted">Loading...</div>
  }
  return <InterfaceSection activeSubsection={active} onSelectSubsection={setActive} />
}

export { dispatchSettingsContributions } from '../dispatch-settings-contributions'

/**
 * Clear every entry in the file-opener registry. Wired into HMR dispose so
 * editing this module (or any module that owns openers) doesn't leave stale
 * duplicates after re-import. Re-registration runs through
 * `applyModuleFileOpeners()` at the next bootstrap.
 */
export function resetFileOpenerRegistryForHmr(): void {
  clearFileOpenerRegistryForHmr()
}

if (import.meta.hot) {
  import.meta.hot.dispose(() => {
    // F2 + F4: the HMR dispose hook must clear BOTH the committed
    // contribution registry AND the legacy adapter's pending buffers, so
    // no stale reserved / active entries leak across HMR re-runs.
    // `resetSettingsContributionsForHmr()` is the canonical single entry
    // point that keeps the write-side registry APIs off this module's
    // import surface (lint-enforced by F4).
    resetSettingsContributionsForHmr()
    resetFileOpenerRegistryForHmr()
    // P3: terminal-link registry must also reset so the next HMR re-run
    // re-evaluates fileMatchersEnabled (which snapshots Editor module
    // enabled state). Without this, toggling Editor in dev mode would
    // leave stale matcher registration until full reload.
    __resetBuiltinTerminalLinks()
  })
}

export function registerBuiltinModules(): void {
  const caps = getPlatformCapabilities()

  // Sync contributors
  registerSyncContributors()

  // Modules with pane renderers
  registerModule({
    id: 'new-tab',
    name: 'New Tab',
    panes: [{ kind: 'new-tab', component: NewTabPaneWrapper }],
  })
  registerModule({
    id: 'session',
    name: 'Session',
    panes: [{ kind: 'tmux-session', component: SessionPaneContent }],
  })
  registerModule({
    id: 'dashboard',
    name: 'Dashboard',
    panes: [{ kind: 'dashboard', component: DashboardPage }],
  })
  registerModule({
    id: 'history',
    name: 'History',
    panes: [{ kind: 'history', component: HistoryPage }],
  })
  registerModule({
    id: 'settings',
    name: 'Settings',
    panes: [{ kind: 'settings', component: SettingsPage }],
  })
  registerModule({
    id: 'browser',
    name: 'Browser',
    disableable: true,
    descriptionKey: 'modules.browser.description',
    panes: [{ kind: 'browser', component: BrowserPaneWrapper }],
    // Spec §I1 — every disableable module must declare at least one
    // purdex-scope settings entry so the Settings sidebar mirrors the
    // Modules Switchboard. Browser has no global config to expose; the
    // shared placeholder renders a neutral "no global settings" message.
    settings: [
      {
        localId: 'browser',
        scope: 'purdex',
        order: SETTINGS_ORDER.MODULE_BROWSER,
        labelKey: 'settings.section.browser',
        component: PlaceholderSettingsSection,
      },
    ],
  })
  registerModule({
    id: 'memory-monitor',
    name: 'Performance Monitor',
    disableable: true,
    descriptionKey: 'modules.memory_monitor.description',
    panes: [{ kind: 'memory-monitor', component: MemoryMonitorPaneWrapper }],
    settings: [{
      localId: 'performance-monitor',
      scope: 'purdex',
      order: SETTINGS_ORDER.MODULE_PERFORMANCE_MONITOR,
      // Sidebar short label switched to settings.section.monitor (spec §4.5);
      // the inner page heading + pane label still use performance_monitor.title.
      labelKey: 'settings.section.monitor',
      component: PerformanceMonitorSettingsSection,
    }],
  })
  registerModule({
    id: 'hosts',
    name: 'Hosts',
    panes: [{ kind: 'hosts', component: HostPage }],
  })
  registerModule({
    id: 'execution',
    name: 'Execution',
    panes: [{ kind: 'execution', component: ExecutionPaneWrapper }],
  })

  // Editor module
  registerModule(editorModuleDefinition)

  // Quick Commands v2 — Phase 1b adds the settings contribution alongside the
  // workspace-context-menu entry point landed in this PR. Settings UI is the
  // sole place to author commands + bindings; the workspace right-click menu
  // surfaces them in the live UI (spec §6 — Settings out + at least one
  // visible mount surface ship together).
  registerModule({
    id: 'quick-commands',
    name: 'Quick Commands',
    disableable: true,
    descriptionKey: 'modules.quick_commands.description',
    settings: [
      {
        localId: 'quick-commands',
        scope: 'purdex',
        order: SETTINGS_ORDER.MODULE_QUICK_COMMANDS,
        // Sidebar short label switched to settings.section.commands
        // (spec §4.5); module.name + inner page still say "Quick Commands".
        labelKey: 'settings.section.commands',
        component: QuickCommandsSettingsSection,
      },
    ],
  })

  // Sync — promoted from a built-in section to a structural module
  // (spec §4.3, PR-2). Intentionally NOT marked `disableable`: turning
  // Sync off requires engine + contributor wiring that is a future spec
  // item. The module-owned route makes Sync render with the puzzle icon
  // in the sidebar, matching its peer surface area (engine, 7
  // contributors, dedicated store).
  registerModule({
    id: 'sync',
    name: 'Sync',
    settings: [
      {
        localId: 'sync',
        scope: 'purdex',
        order: SETTINGS_ORDER.MODULE_SYNC,
        labelKey: 'settings.section.sync',
        component: SyncSection,
      },
    ],
  })

  // FS backends
  registerBuiltinFsBackends(caps)

  registerModule({
    id: 'files',
    name: 'Files',
    disableable: true,
    descriptionKey: 'modules.files.description',
    settings: [
      {
        localId: 'workspace-files',
        scope: 'workspace',
        order: SETTINGS_ORDER.WORKSPACE_FILES,
        labelKey: 'settings.section.files_workspace',
        component: FilesWorkspaceSettingsSection,
      },
      // Spec §I1 — Files has workspace-scope settings but no global ones;
      // the purdex placeholder keeps the Settings sidebar entry alongside
      // the Modules Switchboard row.
      {
        localId: 'files',
        scope: 'purdex',
        order: SETTINGS_ORDER.MODULE_FILES,
        labelKey: 'settings.section.files',
        component: PlaceholderSettingsSection,
      },
    ],
    views: [
      {
        id: 'file-tree-workspace',
        label: 'Files (Workspace)',
        icon: FolderOpen,
        scope: 'workspace',
        component: FileTreeWorkspaceView,
      },
      {
        id: 'file-tree-session',
        label: 'Files (Session)',
        icon: FolderOpen,
        scope: 'tab',
        component: FileTreeSessionView,
      },
    ],
  })

  // Settings sections
  registerSettingsSection({ id: 'appearance', label: 'settings.section.appearance', order: SETTINGS_ORDER.APPEARANCE, component: AppearanceSection })
  registerSettingsSection({ id: 'terminal', label: 'settings.section.terminal', order: SETTINGS_ORDER.TERMINAL, component: TerminalSection })
  registerSettingsSection({
    id: 'interface',
    label: 'settings.section.interface',
    order: SETTINGS_ORDER.INTERFACE,
    component: InterfaceSectionHost,
  })
  // Sync was promoted to a structural module above (spec §4.3, PR-2);
  // its `registerSettingsSection({ id: 'sync', ... })` call lived here
  // and is intentionally removed.
  // Modules Switchboard — replaces the long-dormant `globalConfig` UI with a
  // module enable/disable panel. Keeps the id `module-config` for URL
  // stability (`/settings/module-config`).
  registerSettingsSection({
    id: 'module-config',
    label: 'settings.section.modules',
    order: SETTINGS_ORDER.MODULE_CONFIG,
    component: ModulesSwitchboardSection,
  })

  // Interface subsections
  registerInterfaceSubsection({
    id: 'new-tab',
    label: 'settings.interface.new_tab',
    order: 0,
    component: NewTabSubsection,
  })
  registerInterfaceSubsection({
    id: 'pane',
    label: 'settings.interface.pane',
    order: 1,
    component: () => null,
    disabled: true,
    disabledReason: 'settings.coming_soon',
  })
  registerInterfaceSubsection({
    id: 'sidebar',
    label: 'settings.interface.sidebar',
    order: 2,
    component: () => null,
    disabled: true,
    disabledReason: 'settings.coming_soon',
  })

  // New-tab providers
  registerNewTabProvider({
    id: 'sessions',
    label: 'session.provider_label',
    icon: 'List',
    order: 0,
    component: SessionSection,
  })

  registerEditorNewTabProviders()

  registerNewTabProvider({
    id: 'browser',
    label: 'browser.provider_label',
    icon: 'Globe',
    order: -10,
    component: BrowserNewTabSection,
    disabled: !caps.canBrowserPane,
    disabledReason: 'browser.requires_app',
  })

  if (caps.canSystemTray) {
    registerSettingsSection({
      id: 'electron',
      label: 'settings.section.electron',
      order: SETTINGS_ORDER.ELECTRON,
      component: ElectronSection,
    })
  }

  if (caps.devUpdateEnabled) {
    registerSettingsSection({
      id: 'dev-environment',
      label: 'settings.section.dev_environment',
      order: SETTINGS_ORDER.DEV_ENVIRONMENT,
      component: DevEnvironmentSection,
    })
  }

  if (import.meta.env.DEV || caps.devUpdateEnabled) {
    registerSettingsSection({
      id: 'tmux-agent-monitor',
      label: 'settings.section.tmux_agent_monitor',
      order: SETTINGS_ORDER.TMUX_AGENT_MONITOR,
      component: TmuxAgentMonitorSection,
    })
  }

  // Workspace Snapshot — capture / restore the tab + session layout (Phase 3).
  registerSettingsSection({
    id: 'snapshot',
    label: 'settings.section.snapshot',
    order: SETTINGS_ORDER.SNAPSHOT,
    component: SnapshotSettingsSection,
  })

  registerBuiltinTerminalLinks({
    urlOpener: {
      isElectron: caps.isElectron,
      openBrowserTab,
      openExternal: (url) => window.electronAPI?.openExternalUrl(url),
    },
    filePathOpener: {
      // P5: file-path opener now drives the openFile pipeline (stat → cache
      // → popup) rather than calling tab APIs directly. The pipeline's
      // tabOpener still does getDefaultOpener + openSingletonTab + insertTab.
      tryOpenFile: (file, source, ctx) => tryOpenFileForTerminalLink(file, source, ctx),
      openAsBuffer: (file, source, ctx) => openFileAsBufferDirect(file, source, ctx),
      getActiveWorkspaceId: () => useWorkspaceStore.getState().activeWorkspaceId,
      // The terminal-link opener resolves a relative path at click time and
      // does not persist the answer against a pane binding, so it takes the
      // cwd alone; the generation stamp is the cwd probe's business (§4.6.2).
      fetchPaneCwd: (hostId, sessionCode, signal) =>
        fetchSessionCwd(hostId, sessionCode, signal).then((r) => r.cwd),
      fetchPaneHome: (hostId, sessionCode, signal) => fetchSessionHome(hostId, sessionCode, signal),
      resolveOpenContextCwd: (hostId, sessionCode) =>
        resolveOpenContextCwdFromSessions(hostId, sessionCode),
    },
    // P3: file-path matchers register together with the Editor module's
    // file-opening capability. When Editor is disabled, no file opener
    // can act on a click, so detecting the path would only produce a
    // clickable-looking but silently no-op link. Reload-required,
    // snapshotted at bootstrap like P1 file openers.
    fileMatchersEnabled: useModuleEnabledStore.getState().isEnabled('editor'),
  })

  // Built-in host sub-page contributions (PR-4 + #586).
  // Atomically replace the full set of built-in host sources; any localId
  // not in this list would be dropped.  Re-materialized by every
  // dispatchSettingsContributions() call (idempotent).  Wrapper identity is
  // stable per localId across HMR reloads.
  setHostBuiltinSections([
    { localId: 'overview',  labelKey: 'hosts.overview',  order: 0, component: OverviewSection },
    { localId: 'sessions',  labelKey: 'hosts.sessions',  order: 1, component: SessionsSection },
    { localId: 'hooks',     labelKey: 'hosts.hooks',     order: 2, component: HooksSection },
    { localId: 'agents',    labelKey: 'hosts.agents',    order: 3, component: AgentsSection },
    { localId: 'uploads',   labelKey: 'hosts.uploads',   order: 4, component: UploadSection },
    { localId: 'logs',      labelKey: 'hosts.logs',      order: 5, component: LogsSection },
  ])

  // Reconcile module-declared file openers with the file-opener registry.
  // Must run after every registerModule(...) call so getModules() returns the
  // fully populated set; iterating earlier would miss late-registered modules.
  // No-op until Task 1.3 promotes Editor's inline file openers into
  // editorModuleDefinition.fileOpeners — at which point this becomes the
  // authoritative wire-up and registerEditorFileOpeners() goes away.
  applyModuleFileOpeners()

  // Capture the module-enabled baseline for the Modules Switchboard. Runs
  // after all registerModule(...) calls so `getModules()` returns the fully
  // populated set with their `disableable` flags resolved. `captureBaseline`
  // is a first-call-wins no-op, so HMR re-runs of this function don't
  // overwrite the session baseline (spec I7).
  const baselineSnapshot: Record<string, boolean> = {}
  for (const m of getModules()) {
    if (m.disableable === true) {
      baselineSnapshot[m.id] = useModuleEnabledStore.getState().isEnabled(m.id)
    }
  }
  useModuleEnabledStore.getState().captureBaseline(baselineSnapshot)

  // Dispatch module-declared settings contributions into the contribution registry.
  // Must run AFTER all registerModule(...) calls so every module is visible.
  dispatchSettingsContributions()
}
