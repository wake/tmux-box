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

/** Minimum gap between two requests for one binding, measured from completion. */
const COOLDOWN_MS = 30_000

/**
 * The defer-never-drop state of one binding (spec §5.4.1).
 *
 * Dropping a suppressed trigger loses the one hook that mattered: a session
 * that emits a single event at t=5 s and then goes idle would be skipped by a
 * bare cooldown and never asked again. So a suppressed trigger is DEFERRED.
 *
 * `nextAllowedAt` is computed ONLY when a request completes, as
 * `completedAt + COOLDOWN_MS`. Later triggers never move it — a debounce would,
 * and a busy session would then starve its own deferred run by pushing the
 * deadline forward forever.
 */
interface Cooldown {
  nextAllowedAt: number
  pending: boolean
  timer: ReturnType<typeof setTimeout> | null
}

const cooldowns = new Map<string, Cooldown>()

function cooldownFor(key: string): Cooldown {
  let cd = cooldowns.get(key)
  if (!cd) {
    cd = { nextAllowedAt: 0, pending: false, timer: null }
    cooldowns.set(key, cd)
  }
  return cd
}

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
 * The one scheduling entry point: every trigger calls this, and it decides
 * whether the request runs now, is deferred, or is merely noted (spec §5.4.1).
 *
 * Coalescing is the point — ten hooks inside one cooldown buy one request.
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
  if (disowned.has(key)) return
  if (!wantsProbe(hostId, sessionCode, tmuxInstance)) return

  const cd = cooldownFor(key)

  // In flight: note the interest and stop. NO timer is armed here — the
  // completion handler owns what happens next, and it is the only place that
  // knows when the next slot opens.
  if (inFlight.has(key)) {
    cd.pending = true
    return
  }

  const now = Date.now()
  if (now >= cd.nextAllowedAt) {
    startRequest(cd, hostId, sessionCode, tmuxInstance, key)
    return
  }

  // Still cooling: defer, never drop. One timer serves however many triggers
  // arrive before it fires.
  cd.pending = true
  if (cd.timer === null) {
    cd.timer = setTimeout(
      () => runDeferred(cd, hostId, sessionCode, tmuxInstance, key),
      cd.nextAllowedAt - now,
    )
  }
}

/**
 * The deferred run re-checks EVERYTHING before spending a request: the pane may
 * have gained an agent, been re-pointed, terminated, been disowned or lost its
 * attach gate while the cooldown ran.
 *
 * The timer handle is cleared whether or not a request follows. A handle left
 * behind would make the scheduler believe a timer is still armed, and every
 * later deferred run would be silently swallowed.
 */
function runDeferred(
  cd: Cooldown,
  hostId: string,
  sessionCode: string,
  tmuxInstance: string,
  key: string,
): void {
  cd.timer = null
  cd.pending = false
  if (inFlight.has(key) || disowned.has(key)) return
  if (Date.now() < cd.nextAllowedAt) return
  if (!canAttachTerminal(hostId)) return
  if (!wantsProbe(hostId, sessionCode, tmuxInstance)) return
  startRequest(cd, hostId, sessionCode, tmuxInstance, key)
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
function startRequest(
  cd: Cooldown,
  hostId: string,
  sessionCode: string,
  tmuxInstance: string,
  key: string,
): void {
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
    .finally(() => {
      inFlight.delete(key)
      // A REJECTED request enters the cooldown exactly like a resolved one,
      // otherwise a host that is briefly down would be hammered.
      cd.nextAllowedAt = Date.now() + COOLDOWN_MS
      // A binding dropped by `resetProvenanceProbes` while this request was out
      // must not leave a timer behind for the next test (or the next session).
      if (cooldowns.get(key) !== cd) return
      // Something asked while this was in flight: schedule it for the NEW
      // deadline rather than running now. Firing on completion would let a slow
      // request with continuous hooks run back to back.
      if (cd.pending && cd.timer === null) {
        cd.timer = setTimeout(
          () => runDeferred(cd, hostId, sessionCode, tmuxInstance, key),
          COOLDOWN_MS,
        )
      }
    })
}

/** Test seam: drop the in-flight, disowned and cooldown state between cases. */
export function resetProvenanceProbes(): void {
  for (const cd of cooldowns.values()) {
    // Without this an armed timer outlives its test and fires inside the next.
    if (cd.timer !== null) clearTimeout(cd.timer)
  }
  cooldowns.clear()
  inFlight.clear()
  disowned.clear()
}
