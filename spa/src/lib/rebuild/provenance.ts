// spa/src/lib/rebuild/provenance.ts — the daemon's `pdx_provenance` envelope,
// validated into the shape the rebuild record is written from (spec §4.3.1).
//
// The envelope is the ONLY thing the write path may read. On a proxy-collapsed
// event the outer `agent_type` names the session-projection winner while the
// rest of the detail describes the sender; re-deriving the agent from that
// field is exactly the mis-attribution the envelope exists to prevent.

/** A validated envelope. Optional fields are '' when the daemon omitted them. */
export interface ParsedProvenance {
  agentType: string
  sessionId: string
  cwd: string
  tmuxPaneId: string
  tmuxInstance: string
}

/** A payload field that is not a string is treated as absent, never coerced. */
function str(v: unknown): string {
  return typeof v === 'string' ? v : ''
}

/**
 * Read `detail.pdx_provenance`. Returns null unless the envelope is a real
 * object, `owner_session_start` is strictly `true`, and both `agent_type` and
 * `tmux_instance` are non-empty — an unknown generation must never write,
 * because a record that cannot name its tmux generation cannot be guarded
 * against session-code reuse (spec §4.5).
 */
export function parseProvenance(
  detail: Record<string, unknown> | undefined,
): ParsedProvenance | null {
  const raw = detail?.pdx_provenance
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) return null
  const env = raw as Record<string, unknown>
  if (env.owner_session_start !== true) return null

  const agentType = str(env.agent_type)
  const tmuxInstance = str(env.tmux_instance)
  if (!agentType || !tmuxInstance) return null

  return {
    agentType,
    sessionId: str(env.session_id),
    cwd: str(env.cwd),
    tmuxPaneId: str(env.tmux_pane_id),
    tmuxInstance,
  }
}
