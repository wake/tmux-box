/**
 * Session ids observed in the wild: cc and codex emit UUIDs, opencode emits a
 * `ses_`-prefixed opaque string. Anything outside this alphabet is refused
 * rather than interpolated into a shell command sent via `send-keys`.
 */
const SAFE_SESSION_ID = /^[A-Za-z0-9_-]{1,128}$/

const EXACT: Record<string, (id: string) => string> = {
  cc: (id) => `claude --resume ${id}`,
  codex: (id) => `codex resume ${id}`,
  opencode: (id) => `opencode -s ${id}`,
}

const CWD_SCOPED: Record<string, string> = {
  cc: 'claude -c',
  codex: 'codex resume --last',
  opencode: 'opencode -c',
}

/**
 * Compose the command that resumes `agentType`'s conversation. Returns '' when
 * the agent is unknown — a pane with no recorded agent rebuilds as a plain
 * shell rather than guessing which agent to run (spec §4.7, §9.1).
 *
 * An unusable session id degrades to the cwd-scoped form, never to an
 * interpolated one.
 */
export function composeResumeCommand(agentType: string, sessionId?: string): string {
  const fallback = Object.prototype.hasOwnProperty.call(CWD_SCOPED, agentType)
    ? CWD_SCOPED[agentType]
    : undefined
  if (!fallback) return ''
  if (sessionId && SAFE_SESSION_ID.test(sessionId)) return EXACT[agentType](sessionId)
  return fallback
}
