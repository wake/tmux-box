import { useCallback, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { CommandSlot } from '../../../components/CommandSlot'
import { HostPickerPopover } from '../../../components/HostPickerPopover'
import { runWorkspaceSlot } from '../../../lib/slot-executor'
import { QUICK_COMMAND_SLOTS } from '../../../lib/quick-command-slots'
import { useTabStore } from '../../../stores/useTabStore'
import { useWorkspaceStore } from '../../../stores/useWorkspaceStore'
import { useQuickCommandStore } from '../../../stores/useQuickCommandStore'
import { useModuleEnabledStore } from '../../../stores/useModuleEnabledStore'
import { useI18nStore } from '../../../stores/useI18nStore'
import { getBindingTargets } from '../../../lib/quick-command-bindings'

interface Props {
  workspaceId: string
  /**
   * hostId 為 null 時 chip 仍顯示，executor 點擊後會開 HostPickerPopover
   * 讓 user 選 host（spec v4 §3.2.2）。
   */
  hostId: string | null
  /**
   * codex round-1 P2 (F3 — picker hover-dismissal) — invoked whenever the
   * internal HostPickerPopover open state flips. The parent hub uses this to
   * suppress its own mouseleave / pointerdown close logic while the picker is
   * up, so moving the pointer toward a host option does not unmount the
   * popover (and the picker with it) before the user can click.
   */
  onPickerOpenChange?: (open: boolean) => void
}

/**
 * Popover chip-list rendered to the LEFT of the Plus-button on each
 * WorkspaceRow on hover/focus. Uses CommandSlot internally — already
 * short-circuits when module disabled / no bindings; we additionally
 * skip rendering the popover wrapper itself in those cases so the
 * hover trigger doesn't expose an empty floating panel.
 *
 * NOTE (spec v4 §3.2.2): we do NOT short-circuit on hostId == null —
 * the picker flow handles that case. Only no-bindings / module-disabled
 * suppress the wrapper.
 */
export function WorkspaceQuickActionsPopover({
  workspaceId,
  hostId,
  onPickerOpenChange,
}: Props) {
  const t = useI18nStore((s) => s.t)
  const moduleEnabled = useModuleEnabledStore((s) => s.isEnabled('quick-commands'))
  const hasBindings = useQuickCommandStore((s) => {
    const cmds = hostId == null ? s.global : s.getCommands(hostId)
    return cmds.some((c) => {
      const targets = getBindingTargets(s.bindings, c.id)
      return targets !== undefined && targets.includes(QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS)
    })
  })
  // codex round-1 P2 (F1 — cwd parity with context-menu) — spec §3.2 says
  // WORKSPACE_ACTIONS sessions inherit `workspace.moduleConfig.files.projectPath`
  // as cwd. Without this, hover-popover commands fall back to `~` while the
  // identical command from the right-click context menu would run in projectPath.
  const cwd = useWorkspaceStore((s) => {
    const ws = s.workspaces.find((w) => w.id === workspaceId)
    const path = ws?.moduleConfig?.['files']?.['projectPath']
    return typeof path === 'string' && path.length > 0 ? path : undefined
  })
  const wrapperRef = useRef<HTMLDivElement>(null)
  // codex round-2 — picker state shape pinned: open implied by resolver !== null.
  const [picker, setPicker] = useState<{
    open: boolean
    resolver: ((id: string | null) => void) | null
    anchor: HTMLElement | null
  } | null>(null)
  // codex round-2 D2 — resolver mirrored in a ref so unmount cleanup and
  // early-return-null cleanup (F1) can settle the Promise without going
  // through React state. setState updaters do not run reliably on unmounted
  // / about-to-return-null components, but a ref is always readable.
  const pendingResolverRef = useRef<((id: string | null) => void) | null>(null)

  // codex round-1 P2 (F2 — double-click race) — when hostId is already known the
  // picker never opens, so `picker?.open` stays false for the entire async
  // createSession + send-keys round-trip. Without an explicit pending flag a
  // fast double-click would queue two executor pipelines and create two sessions
  // for the same command. The ref is the synchronous guard (covers same-tick
  // double-fire before React re-renders); `executing` mirrors it to React state
  // so the disabled UI in CommandSlot updates.
  const executingRef = useRef(false)
  const [executing, setExecuting] = useState(false)

  const settlePicker = useCallback((id: string | null) => {
    const resolver = pendingResolverRef.current
    pendingResolverRef.current = null
    setPicker(null)
    // codex round-3 — sync notify so the parent hub's pickerOpenRef updates
    // before any blur/pointer event fires from the unmounting picker.
    onPickerOpenChange?.(false)
    if (resolver) resolver(id)
  }, [onPickerOpenChange])

  const resolveHostId = useCallback(
    () =>
      new Promise<string | null>((resolve) => {
        pendingResolverRef.current = resolve
        // codex round-3 — sync notify BEFORE setPicker triggers HostPickerPopover
        // render + auto-focus(first option). Without sync notify, pickerOpenRef
        // would still be false when the child popover's focus() steals focus from
        // the chip, causing the hub's onBlurCapture to collapse popover (and
        // unmount the picker) before the user can click anything. The previous
        // useEffect-based notify ran AFTER child effects — too late.
        onPickerOpenChange?.(true)
        setPicker({ open: true, resolver: resolve, anchor: wrapperRef.current })
      }),
    [onPickerOpenChange],
  )

  // codex round-2 — dangling Promise cleanup. The popover lives behind a hover
  // trigger; mouseleave on the parent hub will unmount this component while a
  // resolver may still be pending. Settling via ref (D2) so cleanup remains
  // reliable even if React has already torn down the component.
  useEffect(() => {
    return () => {
      const resolver = pendingResolverRef.current
      pendingResolverRef.current = null
      if (resolver) resolver(null)
    }
  }, [])

  // codex round-1 P2 (F3 — picker hover-dismissal) / round-3 — picker open
  // notification is now sync'd from resolveHostId/settlePicker (see above) so
  // the hub's pickerOpenRef is updated BEFORE child focus/blur events fire.
  // The useEffect-based notify shipped initially is gone for that reason.
  // We still need an unmount-cleanup notify to clear the hub's ref if the
  // popover is torn down externally (mouseleave) without going through
  // settlePicker first — e.g. picker never opened, or picker was open and
  // unmount cleanup runs before settlePicker can.
  const pickerOpen = picker?.open ?? false
  useEffect(() => {
    return () => onPickerOpenChange?.(false)
  }, [onPickerOpenChange])

  // codex round-2 F1 — early-return-null does NOT unmount the component, so the
  // unmount-cleanup `useEffect` above never fires when bindings disappear or
  // the module is disabled mid-pick. That leaves the picker resolver hanging
  // forever (executor's await never resolves; busy state sticks; pointer/blur
  // close paths stay suppressed). Detect the transition and settle ourselves.
  const visible = moduleEnabled && hasBindings
  useEffect(() => {
    if (!visible) {
      settlePicker(null)
      // keep executing flag in sync; if executor was past resolveHostId we
      // can't unwind it, but the resolver-null path will short-circuit it.
      if (executingRef.current && !pickerOpen) {
        // Executor was past the picker stage — let it finish naturally.
      }
    }
  }, [visible, pickerOpen, settlePicker])

  // codex round-2 A1 — workspace-deletion transaction safety (parity with
  // WorkspaceQuickCommandsContextMenu). createSession + send-keys are async;
  // the workspace can be deleted in that window. Without pre-check / read-back
  // / rollback, openSingletonTab would still create a tab while insertTab
  // silently no-ops (workspace gone), then setActive* would mutate state for
  // a nonexistent workspace and leave an orphan tab unattached anywhere.
  const switchToSession = useCallback(
    (h: string, sessionCode: string) => {
      // (1) pre-check — fail fast if the workspace is already gone.
      if (
        !useWorkspaceStore.getState().workspaces.some((w) => w.id === workspaceId)
      ) {
        throw new Error(
          `WorkspaceQuickActionsPopover: workspace ${workspaceId} no longer exists; aborting before tab creation`,
        )
      }
      // Snapshot for rollback when the residual race fires.
      const prevActiveTabId = useTabStore.getState().activeTabId
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
        // active-tab mutation. Throw so trySwitch surfaces switch_failed.
        useTabStore.getState().closeTab(tabId)
        useTabStore.getState().setActiveTab(prevActiveTabId)
        throw new Error(
          `WorkspaceQuickActionsPopover: workspace ${workspaceId} deleted mid-flight; rolled back tab ${tabId}`,
        )
      }
      useWorkspaceStore.getState().setActiveWorkspace(workspaceId)
      useTabStore.getState().setActiveTab(tabId)
    },
    [workspaceId],
  )

  if (!visible) return null

  return (
    <div
      ref={wrapperRef}
      role="group"
      // codex round-1 C16 — i18n key, not hard-coded English
      aria-label={t('quick_commands.aria.workspace_actions')}
      className="absolute right-full top-1/2 -translate-y-1/2 mr-1 flex items-center gap-1 px-2 py-1 rounded-md bg-gradient-to-l from-surface-secondary/0 to-surface-secondary/95 backdrop-blur-sm shadow-md z-30"
    >
      <CommandSlot
        mountTo={QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS}
        // codex round-1 P2 (F1) — pass cwd so hover-popover commands inherit
        // workspace projectPath, matching context-menu behavior.
        ctx={{ hostId, workspaceId, cwd }}
        // codex round-1 C11 + P2 (F2) — busy=true during picker mid-flight OR
        // executor mid-flight; without `executing` the hostId-known path would
        // never disable chips, allowing double-click duplicate session creation.
        busy={pickerOpen || executing}
        executor={async (cmd, ctx) => {
          // codex round-1 P2 (F2) — synchronous ref guard catches the
          // double-click window before React re-renders the disabled state.
          if (executingRef.current) return
          executingRef.current = true
          setExecuting(true)
          try {
            await runWorkspaceSlot(
              cmd,
              { ...ctx, workspaceId },
              {
                switchToSession,
                resolveHostId,
                // #690 enforcement (alpha.242) — workspace liveness probe is
                // type-level required on Deps. Workspace gets unmounted async
                // (mouseleave / route change) while createSession is in flight;
                // returning false here aborts before send-keys.
                assertContextLive: () =>
                  useWorkspaceStore.getState().workspaces.some((w) => w.id === workspaceId),
              },
            )
          } finally {
            executingRef.current = false
            setExecuting(false)
          }
        }}
      />
      {/*
        codex round-2 D1 — HostPickerPopover uses position:fixed sourced from
        anchor.getBoundingClientRect(). Inside our transformed wrapper
        (-translate-y-1/2) a fixed descendant is positioned relative to that
        ancestor instead of the viewport, throwing the picker off by tens of
        pixels in real browsers (jsdom tests don't reproduce). Portal to body
        so the picker escapes the transformed subtree while still receiving
        the same DOM anchor for coordinate computation.
      */}
      {createPortal(
        <HostPickerPopover
          open={pickerOpen}
          anchor={picker?.anchor ?? null}
          onSelect={(hid) => settlePicker(hid)}
          onCancel={() => settlePicker(null)}
        />,
        document.body,
      )}
    </div>
  )
}
