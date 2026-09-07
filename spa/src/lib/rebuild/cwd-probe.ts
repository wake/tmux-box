// spa/src/lib/rebuild/cwd-probe.ts — the shell-only cwd baseline (spec §4.4).
//
// A pane that never saw a qualifying SessionStart still needs a directory to
// rebuild into. Two triggers capture it, because neither covers the other:
//
//   1. a reconciled `sessions` payload (`useMultiHostEventWs`) — sweeps every
//      pane on the host, but only fires when the session list changes;
//   2. pane attach (`SessionPaneContent`) — covers a pane opened after the
//      list has settled, which gets no further broadcast.
//
// Both go through `probeSessionCwd`, whose in-flight set is keyed by the pane
// binding, so the two triggers can never fire the same request twice.
//
// Neither trigger may run before the host's attach gate has opened, and the
// daemon's answer carries the generation it was sampled in, which must equal
// the binding the probe asked with (spec §4.6.2). Both rules exist for one
// reason: a session code is a reversible encoding of `$N`, so an answer the
// daemon resolved by code alone is not by itself attributable to a binding.
import { fetchSessionCwd } from '../host-api'
import { generationMatchesLegacy } from './binding'
import { useTabStore } from '../../stores/useTabStore'
import { scanPaneTree } from '../pane-tree'
import { canAttachTerminal } from './attach-gate'

/** One probe per `(hostId, sessionCode, tmuxInstance)` binding at a time. */
const inFlight = new Set<string>()

/**
 * Bindings the daemon has conclusively disowned: it answered with a different
 * non-empty generation, so the pane's code now belongs to a stranger and the
 * next `sessions` payload will mark the pane dead. Until then, every broadcast
 * would otherwise re-ask the same refused question.
 *
 * An *unknown* (`''`) answer is deliberately NOT recorded here. It is transient
 * — a `tmux display-message` timeout, or a generation that moved during the
 * read — so a later attempt can still succeed. Unknown blocks the write; it
 * does not condemn the binding.
 */
const disowned = new Set<string>()

// NUL separates the parts of a composite key below: it cannot occur in a host
// id, a session code or an instance stamp, so no two distinct triples collide.
// It is written as the `\u0000` escape rather than as a raw byte — a literal
// NUL makes the file count as binary, which puts it out of grep's reach.
const bindingKey = (hostId: string, sessionCode: string, tmuxInstance: string) =>
  `${hostId}\u0000${sessionCode}\u0000${tmuxInstance}`

/** A pane that would take a probed cwd: live, terminal-mode, and cwd-less. */
function wantsProbe(
  hostId: string,
  sessionCode: string,
  tmuxInstance: string,
): boolean {
  let found = false
  for (const tab of Object.values(useTabStore.getState().tabs)) {
    scanPaneTree(tab.layout, (pane) => {
      const c = pane.content
      if (found) return
      if (c.kind !== 'tmux-session' || c.mode !== 'terminal' || c.terminated) return
      if (c.hostId !== hostId || c.sessionCode !== sessionCode) return
      // The legacy-compatible rule (`binding.ts`), the same one the store's
      // write uses: a pane whose recorded instance is '' has not learnt its
      // generation yet.
      if (!generationMatchesLegacy(c.tmuxInstance, tmuxInstance)) return
      if (c.rebuild?.cwd) return
      found = true
    })
    if (found) return true
  }
  return false
}

/**
 * Probe the session's cwd and record it as the `pane-probe` baseline, unless
 * agent provenance (or a user edit) already supplied one.
 *
 * The pane is re-read when the request resolves: a pane re-pointed, terminated
 * or filled in while the request was in flight no longer wants this answer.
 * That re-read only asks whether SOME pane still wants it, though — the write
 * is session-scoped, so the per-pane decision (terminated included) is made by
 * the store inside the same `set` that writes.
 *
 * Two generation preconditions sit either side of the request (spec §4.6.2):
 * the host's attach gate must be open before it is sent, and the generation
 * stamped on the answer must be non-empty AND equal to the one the probe asked
 * with before it is written. Anything else — a different generation, or an
 * unknown one on either side — is discarded, because a cwd that cannot be
 * attributed to this binding would become the directory a rebuild launches the
 * agent in. A pane carrying no generation therefore records no probed cwd
 * until it adopts one from a `sessions` payload (`reconcile.ts`).
 */
export function probeSessionCwd(hostId: string, sessionCode: string, tmuxInstance: string): void {
  if (!hostId || !sessionCode) return
  // The gate is the same one the terminal attach waits on: until this
  // connection's first `sessions` payload has been reconciled, there is no
  // evidence about which generation owns this code.
  if (!canAttachTerminal(hostId)) return
  const key = bindingKey(hostId, sessionCode, tmuxInstance)
  if (inFlight.has(key) || disowned.has(key)) return
  if (!wantsProbe(hostId, sessionCode, tmuxInstance)) return

  inFlight.add(key)
  fetchSessionCwd(hostId, sessionCode)
    .then(({ cwd, tmuxInstance: answered }) => {
      if (answered === '' || answered !== tmuxInstance) {
        // The write needs a POSITIVE match: a non-empty answered generation
        // equal to the one asked with. '' on the answer is the daemon's "I
        // could not tell" — its two-sided sampling disagreed, or
        // `tmux display-message` timed out — and an unknown generation
        // authorises nothing, not even for a pane that has none of its own.
        //
        // Only a different NON-EMPTY generation is proof the code was reused,
        // so only that stops the asking; unknown stays retryable.
        if (answered !== '' && tmuxInstance !== '') disowned.add(key)
        return
      }
      if (!cwd) return
      if (!wantsProbe(hostId, sessionCode, tmuxInstance)) return
      useTabStore.getState().setPaneRebuild(hostId, sessionCode, tmuxInstance, {
        kind: 'probe-cwd', cwd,
      })
    })
    .catch(() => { /* a host that cannot answer just leaves cwd unset */ })
    .finally(() => { inFlight.delete(key) })
}

/** Probe every distinct binding on `hostId` whose record still has no cwd. */
export function probeMissingCwds(hostId: string): void {
  const bindings = new Map<string, { sessionCode: string; tmuxInstance: string }>()
  for (const tab of Object.values(useTabStore.getState().tabs)) {
    scanPaneTree(tab.layout, (pane) => {
      const c = pane.content
      if (c.kind !== 'tmux-session' || c.mode !== 'terminal' || c.terminated) return
      if (c.hostId !== hostId || c.rebuild?.cwd) return
      bindings.set(`${c.sessionCode}\u0000${c.tmuxInstance}`, {
        sessionCode: c.sessionCode, tmuxInstance: c.tmuxInstance,
      })
    })
  }
  for (const { sessionCode, tmuxInstance } of bindings.values()) {
    probeSessionCwd(hostId, sessionCode, tmuxInstance)
  }
}

/** Test seam: drop the in-flight and disowned sets between cases. */
export function resetCwdProbes(): void {
  inFlight.clear()
  disowned.clear()
}
