import type { PaneRebuildRecord } from '../../types/tab'
import type { ResumeTemplateLookup } from '../../stores/useResumeTemplateStore'

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

/**
 * The command this record's rebuild would send, in three layers (spec §4.2):
 *
 *  1. the pane's own override, verbatim — the user typed it, so nothing is
 *     substituted into it and nothing validates its shape;
 *  2. the agent's template pair — `exact` with every `{id}` replaced when the
 *     session id passes `SAFE_SESSION_ID`, otherwise `fallback` **verbatim**,
 *     `{id}` and all: the fallback is by definition the form that has no id to
 *     interpolate, so a brace the user left there is their literal text;
 *  3. `''` — no agent, or an agent nobody has a template for. That is what
 *     makes an unknown agent rebuild as a plain shell rather than a guess.
 *
 * An unusable session id degrades to `fallback`, never to an interpolated
 * command: the string is sent through `send-keys` and an id outside the
 * alphabet is the one thing that could break out of it.
 */
export function resolveResumeCommand(
  record: Pick<PaneRebuildRecord, 'agent' | 'resumeCommandOverride'> | undefined,
  templates: ResumeTemplateLookup,
): string {
  if (record?.resumeCommandOverride) return record.resumeCommandOverride
  const agent = record?.agent
  if (!agent?.type) return ''
  const pair = templates(agent.type)
  if (!pair) return ''
  const id = agent.sessionId
  if (id && SAFE_SESSION_ID.test(id)) return pair.exact.replaceAll('{id}', id)
  return pair.fallback
}
