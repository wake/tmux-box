// spa/src/lib/rebuild/attach-gate.ts — the terminal attach gate (spec §4.6).
//
// Session codes are reused across tmux server restarts, so a pane that attaches
// before the current host-events connection has delivered its first `sessions`
// payload can land on a stranger that inherited its code. The gate is per host
// connection, not per boot: health recovery flips a host to `connected` before
// host-events reconnects, so "the host is up" is not evidence that the pane's
// binding has been re-checked.
//
// It blocks attaching, never rendering — an offline host keeps showing its
// existing offline state, and health/event connections are independent of the
// terminal WS, so a closed gate cannot deadlock a host.
import { useHostStore } from '../../stores/useHostStore'

/** True when `hostId` has reconciled a payload from its live connection. */
export function canAttachTerminal(hostId?: string): boolean {
  if (!hostId) return true // no host binding to verify (legacy single-host URL)
  return useHostStore.getState().runtime[hostId]?.attachReady === true
}

/** A `sessions` payload from the live connection has been reconciled. */
export function openAttachGate(hostId: string): void {
  useHostStore.getState().setRuntime(hostId, { attachReady: true })
}

/** A connection is being (re)started, or has dropped. */
export function closeAttachGate(hostId: string): void {
  useHostStore.getState().setRuntime(hostId, { attachReady: false })
}
