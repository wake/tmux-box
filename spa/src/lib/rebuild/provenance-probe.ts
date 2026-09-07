// spa/src/lib/rebuild/provenance-probe.ts — "who owns this pane?" (spec §5.4).
//
// A sibling of `cwd-probe.ts`, and deliberately built from the SAME named
// helpers rather than a paraphrase of its rules: the two probes ask different
// questions of the same daemon under the same generation hazards, and the one
// time those rules were written out twice they drifted apart.
//
// Where the cwd probe fills a directory, this one fills the agent: a session
// that started before the frame layer learnt to record its own session id has
// no `rebuild.agent`, so its rebuild would launch a plain shell. The daemon can
// still answer, because the frames and the process tree outlive the SPA's
// ignorance — so the SPA asks (spec §5.1: a request, not a broadcast).
//
// Two comparisons here look alike and are NOT the same test:
//
//   * PANE ELIGIBILITY uses `generationMatchesLegacy`, which is one-way — a
//     pane whose recorded instance is '' matches a known expected generation,
//     exactly as broadcasts have always treated it;
//   * AUTHORISING THE WRITE requires the ANSWERED generation to be non-empty
//     AND equal to the one asked with. An unknown generation authorises
//     nothing, not even for a pane that has none of its own.
//
// Merging them would let an unattributable answer name the agent a rebuild
// launches.
import { fetchSessionProvenance } from '../host-api'
import { generationMatchesLegacy } from './binding'
import { useTabStore } from '../../stores/useTabStore'
import { scanPaneTree } from '../pane-tree'
import { canAttachTerminal } from './attach-gate'

/** One request per `(hostId, sessionCode, tmuxInstance)` binding at a time. */
const inFlight = new Set<string>()

/**
 * Bindings the daemon has conclusively disowned: it answered with a different
 * non-empty generation, so the pane's code now belongs to a stranger.
 *
 * An *unknown* ('') generation — on either side — is deliberately NOT recorded
 * here. It blocks the write but is transient, so a later attempt can still
 * succeed. Only a different NON-EMPTY generation is proof the code was reused.
 */
const disowned = new Set<string>()

// NUL separates the parts of a composite key: it cannot occur in a host id, a
// session code or an instance stamp, so no two distinct triples collide. It is
// written as the `\u0000` escape rather than as a raw byte — a literal NUL
// makes the file count as binary, which puts it out of grep's reach.
const bindingKey = (hostId: string, sessionCode: string, tmuxInstance: string) =>
  `${hostId}\u0000${sessionCode}\u0000${tmuxInstance}`

/**
 * A pane that would take an ownership answer: live, terminal-mode, generation-
 * eligible, and either agent-less or flagged `unverified`.
 *
 * Nothing else makes a pane eligible. A record with a confirmed agent never
 * asks again, which is what makes the whole thing terminate (spec §5.5).
 */
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
      if (c.rebuild?.agent && !c.rebuild.unverified) return
      found = true
    })
    if (found) return true
  }
  return false
}

/**
 * Ask the daemon which agent owns this pane and apply the answer as an
 * `agent-backfill` patch (spec §5.5's four ordered modes).
 *
 * The pane set is re-read when the request resolves: a pane re-pointed,
 * terminated or filled in while the request was in flight no longer wants this
 * answer. That re-read only asks whether SOME pane still wants it, though —
 * the write is session-scoped, so the per-pane decision is made by the store
 * inside the same `set` that writes.
 */
export function probeSessionProvenance(
  hostId: string,
  sessionCode: string,
  tmuxInstance: string,
): void {
  if (!hostId || !sessionCode) return
  // The gate is the same one the terminal attach waits on: until this
  // connection's first `sessions` payload has been reconciled, there is no
  // evidence about which generation owns this code.
  if (!canAttachTerminal(hostId)) return
  const key = bindingKey(hostId, sessionCode, tmuxInstance)
  if (inFlight.has(key) || disowned.has(key)) return
  if (!wantsProbe(hostId, sessionCode, tmuxInstance)) return

  inFlight.add(key)
  fetchSessionProvenance(hostId, sessionCode)
    .then((ans) => {
      if (ans.tmuxInstance === '' || ans.tmuxInstance !== tmuxInstance) {
        // The write needs a POSITIVE match. '' on the answer is the daemon's
        // "I could not tell" — its two-sided sampling disagreed, or
        // `tmux display-message` timed out.
        //
        // Only a different NON-EMPTY generation on BOTH sides is proof the code
        // was reused, so only that stops the asking.
        if (ans.tmuxInstance !== '' && tmuxInstance !== '') disowned.add(key)
        return
      }
      if (!ans.found || !ans.agentType) return
      if (!wantsProbe(hostId, sessionCode, tmuxInstance)) return
      useTabStore.getState().setPaneRebuild(hostId, sessionCode, tmuxInstance, {
        kind: 'agent-backfill',
        record: {
          tmuxInstance: ans.tmuxInstance,
          agent: {
            type: ans.agentType,
            sessionId: ans.sessionId || undefined,
            tmuxPaneId: ans.tmuxPaneId || undefined,
            updatedAt: ans.lastSeenAt || Date.now(),
          },
          ...(ans.cwd ? { cwd: ans.cwd } : {}),
        },
      })
    })
    .catch(() => { /* a host that cannot answer just leaves the record alone */ })
    .finally(() => { inFlight.delete(key) })
}

/** Test seam: drop the in-flight and disowned sets between cases. */
export function resetProvenanceProbes(): void {
  inFlight.clear()
  disowned.clear()
}
