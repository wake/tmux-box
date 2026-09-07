// spa/src/lib/rebuild/binding.ts — the pane binding, and the TWO ways to
// compare it (spec §4.1 / §4.5).
//
// A pane is bound to `(hostId, sessionCode, tmuxInstance)`, not to
// `(hostId, sessionCode)`: a session code is a reversible encoding of the tmux
// id `$N`, so after a tmux server restart `$0` mints the same code again and
// host+code alone would let a payload from a new tmux server land on a pane
// bound to the old one.
//
// The two comparisons differ only in how they treat an EMPTY RECORDED
// generation, and they exist for different jobs:
//
//   * {@link bindingEquals} — exact. Used where the caller planned an
//     operation from a concrete binding it observed, and must refuse to act if
//     the pane no longer holds exactly that one. `''` is a value like any
//     other there: a pane that has since learnt its generation has moved.
//   * {@link bindingMatchesLegacy} — an empty RECORDED instance is a wildcard,
//     so a pane that has not learnt its generation yet (a pane restored from an
//     older snapshot, or one on a host whose `tmux display-message` timed out)
//     still receives broadcasts addressed to any generation, exactly as it did
//     before generations existed. The wildcard is one-directional on purpose:
//     an empty EXPECTED instance narrows to panes that are equally unknown, it
//     does not become a broadcast to everyone.
//
// Several places used to re-implement one or the other inline, which is how
// the probe's read rule and the store's write rule came to disagree.

/** Where a pane points: a session on a host, under one tmux generation. */
export interface SessionBinding {
  hostId: string
  sessionCode: string
  tmuxInstance: string
}

/** Two bindings describe the same session under the same tmux generation. */
export function bindingEquals(a: SessionBinding, b: SessionBinding): boolean {
  return a.hostId === b.hostId
    && a.sessionCode === b.sessionCode
    && a.tmuxInstance === b.tmuxInstance
}

/**
 * The generation half of the legacy-compatible rule: a pane that recorded no
 * generation answers to any expected one.
 */
export function generationMatchesLegacy(recorded: string, expected: string): boolean {
  return recorded === '' || recorded === expected
}

/**
 * The whole binding under the legacy-compatible rule — host and code exact,
 * generation by {@link generationMatchesLegacy}.
 */
export function bindingMatchesLegacy(recorded: SessionBinding, expected: SessionBinding): boolean {
  return recorded.hostId === expected.hostId
    && recorded.sessionCode === expected.sessionCode
    && generationMatchesLegacy(recorded.tmuxInstance, expected.tmuxInstance)
}
