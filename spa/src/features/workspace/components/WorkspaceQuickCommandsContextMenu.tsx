import { useCallback, useEffect, useRef, useState } from 'react'
import { CommandSlot } from '../../../components/CommandSlot'
import { HostPickerPopover } from '../../../components/HostPickerPopover'
import { runWorkspaceSlot } from '../../../lib/slot-executor'
import { QUICK_COMMAND_SLOTS } from '../../../lib/quick-command-slots'
import { useTabStore } from '../../../stores/useTabStore'
import { useWorkspaceStore } from '../../../stores/useWorkspaceStore'
import { useQuickCommandStore } from '../../../stores/useQuickCommandStore'
import { useModuleEnabledStore } from '../../../stores/useModuleEnabledStore'
import { getBindingTargets } from '../../../lib/quick-command-bindings'

interface Props {
  workspaceId: string
  /**
   * Workspace 的多數決 hostId（spec v4 §3.2.1）；null 代表 workspace 無
   * tmux-session tabs，executor 會在 callback 裡開 HostPickerPopover。
   */
  hostId: string | null
  onClose: () => void
}

/**
 * 渲染 mount=WORKSPACE_ACTIONS 的 quick commands，作為 WorkspaceContextMenu 的子 section。
 *
 * codex round-1 B7 — 不傳 `render` prop（會與 `executor` 衝突 — render 包出來的
 * 是 `<span>`，沒有 onClick，executor 不會跑）。改用 `<CommandSlot>` default
 * button render + `containerClassName="flex flex-col"` 改 layout 為 menu 條列。
 *
 * codex round-1 B5/B6 — switchToSession callback 必須做 `openSingletonAndSelect`
 * 等價邏輯：open singleton tab → insertTab to workspace → setActiveWorkspace +
 * setActiveTab。tmux-session content 欄位要齊全（mode / cachedName / tmuxInstance）
 * 以滿足 `spa/src/types/tab.ts` 的型別契約。
 */
export function WorkspaceQuickCommandsContextMenu({ workspaceId, hostId, onClose }: Props) {
  // Mirror the parent menu's gating: disabling the module or removing every
  // WORKSPACE_ACTIONS binding makes this section disappear entirely (no
  // wrapper <div>, no separator). Hooks must run before the early return so
  // React's hook order stays stable across re-renders.
  const moduleEnabled = useModuleEnabledStore((s) => s.isEnabled('quick-commands'))
  // codex round-1 P2 — own-property guard: capability ids colliding with
  // inherited Object.prototype methods would otherwise crash the slot.
  const hasBindings = useQuickCommandStore((s) => {
    const cmds = hostId == null ? s.global : s.getCommands(hostId)
    return cmds.some((c) => {
      const targets = getBindingTargets(s.bindings, c.id)
      return targets !== undefined && targets.includes(QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS)
    })
  })
  // codex round-2 — spec §3.2 says WORKSPACE_ACTIONS sessions inherit
  // `workspace.moduleConfig.files.projectPath` as cwd. Without this, slot
  // execution falls back to `~` and right-click commands run in the wrong
  // filesystem context. The selector returns `undefined` when the workspace
  // has no projectPath configured; slot-executor then defaults to `~`.
  const cwd = useWorkspaceStore((s) => {
    const ws = s.workspaces.find((w) => w.id === workspaceId)
    const path = ws?.moduleConfig?.['files']?.['projectPath']
    return typeof path === 'string' && path.length > 0 ? path : undefined
  })

  // codex round-2 — picker state shape pinned: open implied by resolver !== null;
  // resolver is always nulled-out the moment it's invoked (idempotent guard against
  // duplicate resolve which would no-op the Promise but is still a sign of a bug).
  const [picker, setPicker] = useState<{
    open: boolean
    resolver: ((hostId: string | null) => void) | null
    anchor: { x: number; y: number } | null
  } | null>(null)
  const lastClickPos = useRef<{ x: number; y: number } | null>(null)
  // Mirrors the latest resolver so unmount cleanup can resolve a pending
  // picker Promise without going through React state — `setPicker` updaters
  // do not run reliably on already-unmounted components.
  const pendingResolverRef = useRef<((hostId: string | null) => void) | null>(null)

  // codex round-1 P2 — double-click race guard. When `hostId` is already known
  // the picker never opens, so `picker?.open` stays false for the entire
  // createSession + send-keys round-trip. Without an explicit pending flag a
  // fast double-click would queue two executor pipelines and create two
  // sessions for the same command. The ref is the synchronous guard (covers
  // the same-tick double-fire that happens before React re-renders); the
  // `executing` state drives the disabled UI in CommandSlot.
  const executingRef = useRef(false)
  const [executing, setExecuting] = useState(false)

  const resolveHostId = useCallback(
    () =>
      new Promise<string | null>((resolve) => {
        pendingResolverRef.current = resolve
        setPicker({ open: true, resolver: resolve, anchor: lastClickPos.current })
      }),
    [],
  )

  // codex round-2 — dangling Promise cleanup. If the parent menu closes (unmount)
  // or the popover gets force-dismissed externally while a picker resolver is
  // still pending, we MUST resolve it as null so the executor's await returns,
  // its `finally` runs, and onClose fires. Otherwise the Promise hangs forever
  // and the executor stays mid-flight (busy=true sticks, chips stay disabled).
  useEffect(() => {
    return () => {
      const resolver = pendingResolverRef.current
      pendingResolverRef.current = null
      if (resolver) resolver(null)
    }
  }, [])

  // codex round-1 B6 — workspace caller must perform full openSingletonAndSelect
  // equivalent (the helper exists at spa/src/features/workspace/hooks.ts but is
  // bound to the hook, so we replicate inline here using the same store
  // primitives it uses).
  //
  // codex round-2 (high) + round-3 (high) — concurrent-delete transaction
  // safety. createSession is async. If the workspace is deleted while it's in
  // flight:
  //   1. pre-check (closes most race windows before they open).
  //   2. read-back guard after insertTab catches the residual race.
  //   3. rollback on residual race: close the orphan tab and restore the
  //      previous activeTabId so the deleted-workspace path leaves NO
  //      observable mutation in tabStore. (round-2 only restored
  //      activeWorkspaceId; the orphan tab and activeTabId mutation persisted.)
  const switchToSession = useCallback(
    (h: string, sessionCode: string) => {
      // (1) pre-check — fail fast if workspace is already gone.
      if (!useWorkspaceStore.getState().workspaces.some((w) => w.id === workspaceId)) {
        throw new Error(
          `WorkspaceQuickCommandsContextMenu: workspace ${workspaceId} no longer exists; aborting before tab creation`,
        )
      }
      // Snapshot for rollback if the residual race fires.
      const prevActiveTabId = useTabStore.getState().activeTabId
      // codex round-1 B5 — fill ALL tmux-session content fields per types/tab.ts
      const tabId = useTabStore.getState().openSingletonTab({
        kind: 'tmux-session',
        hostId: h,
        sessionCode,
        mode: 'terminal',
        cachedName: sessionCode,
        // No Session payload in hand (the slot executor hands back a bare
        // code), so the generation stays unknown until the next sessions
        // payload adopts it (spec §4.5).
        tmuxInstance: '',
      })
      useWorkspaceStore.getState().insertTab(tabId, workspaceId)
      // (2) read-back — workspace may have been deleted between the pre-check
      // and insertTab.
      const inserted =
        useWorkspaceStore
          .getState()
          .workspaces.find((w) => w.id === workspaceId)
          ?.tabs.includes(tabId) ?? false
      if (!inserted) {
        // (3) rollback — close the freshly-created tab and restore the prior
        // activeTabId so the failed switch leaves no orphan + no dangling
        // active-tab mutation. closeTab is a no-op on a locked tab, but the
        // tabs we create here are unlocked by default (createTab default).
        useTabStore.getState().closeTab(tabId)
        useTabStore.getState().setActiveTab(prevActiveTabId)
        throw new Error(
          `WorkspaceQuickCommandsContextMenu: workspace ${workspaceId} deleted mid-flight; rolled back tab ${tabId}`,
        )
      }
      useWorkspaceStore.getState().setActiveWorkspace(workspaceId)
      useTabStore.getState().setActiveTab(tabId)
    },
    [workspaceId],
  )

  // Short-circuit AFTER all hooks are declared so hook order stays stable.
  if (!moduleEnabled || !hasBindings) return null

  return (
    <div
      className="py-1"
      onClickCapture={(e) => {
        // Capture coordinates for picker anchor (fixed-positioned next to click).
        lastClickPos.current = { x: e.clientX, y: e.clientY }
      }}
    >
      <CommandSlot
        mountTo={QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS}
        ctx={{ hostId, workspaceId, cwd }}
        // codex round-1 B7 — flex-col override; default chip render keeps onClick + executor wiring intact.
        containerClassName="flex flex-col"
        // codex round-1 C11 + P2 — busy=true during picker mid-flight OR
        // executor mid-flight; without `executing` the picker-less hostId
        // path would never disable chips, allowing double-click duplicate
        // session creation.
        busy={(picker?.open ?? false) || executing}
        executor={async (cmd, ctx) => {
          // codex round-1 P2 — synchronous ref guard catches the double-click
          // window before React re-renders the disabled state. `executing` is
          // mirrored to React state below for the disabled UI.
          if (executingRef.current) return
          // #690 round-2 D1 — runWorkspaceSlot now requires `ctx.workspaceId`
          // as a non-null string. WORKSPACE_ACTIONS slots only mount inside
          // workspace context, so this is a contract guarantee, not a
          // user-facing error path. Use the closure `workspaceId` (typed as
          // string from props) to satisfy the narrowed type.
          executingRef.current = true
          setExecuting(true)
          try {
            await runWorkspaceSlot(cmd, { ...ctx, workspaceId }, {
              switchToSession,
              resolveHostId,
              // codex round-4 — workspace liveness probe; called by executor
              // between createSession and executeCommand so destructive
              // commands don't ship to a session whose workspace already
              // disappeared.
              assertContextLive: () =>
                useWorkspaceStore.getState().workspaces.some((w) => w.id === workspaceId),
            })
          } finally {
            executingRef.current = false
            // setState on an unmounted component is a silent no-op in React 18,
            // so we don't need to guard against the parent's onClose() racing
            // ahead of this finally block.
            setExecuting(false)
            onClose()
          }
        }}
      />
      <HostPickerPopover
        open={picker?.open ?? false}
        anchor={picker?.anchor ?? null}
        onSelect={(hostId) => {
          // codex round-2 — null-out resolver before invoking to make duplicate-call safe
          const resolver = picker?.resolver
          pendingResolverRef.current = null
          setPicker(null)
          resolver?.(hostId)
        }}
        onCancel={() => {
          const resolver = picker?.resolver
          pendingResolverRef.current = null
          setPicker(null)
          resolver?.(null)
        }}
      />
    </div>
  )
}
