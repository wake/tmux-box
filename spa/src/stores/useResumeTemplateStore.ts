// spa/src/stores/useResumeTemplateStore.ts — the per-agent resume command
// templates (spec §4.1).
//
// The composed `claude --resume <id>` calls the program named `claude`. A user
// who launches Claude Code through a shell function or a wrapper needs a
// different word, and there is no way to guess it — so the command shapes stop
// being hardcoded and become editable per agent.
//
// The store is SPARSE on purpose: only an agent the user actually customised
// gets a record, so `DEFAULT_RESUME_TEMPLATES` stays the answer for everyone
// else and a later change to a default reaches every untouched install.
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'

export interface ResumeTemplatePair {
  /** Used when the record has a usable session id. Should contain `{id}`. */
  exact: string
  /** Used when it does not — taken verbatim, `{id}` included if present. */
  fallback: string
}

/** How a consumer asks for an agent's pair; `undefined` means "no template". */
export type ResumeTemplateLookup = (agentType: string) => ResumeTemplatePair | undefined

/**
 * The shapes that shipped hardcoded, reproduced verbatim: a user who
 * configures nothing sees no change at all.
 */
export const DEFAULT_RESUME_TEMPLATES: Readonly<Record<string, ResumeTemplatePair>> = Object.freeze({
  cc: Object.freeze({ exact: 'claude --resume {id}', fallback: 'claude -c' }),
  codex: Object.freeze({ exact: 'codex resume {id}', fallback: 'codex resume --last' }),
  opencode: Object.freeze({ exact: 'opencode -s {id}', fallback: 'opencode -c' }),
})

/** An agent nobody has a shape for: the user may still teach the store one. */
const BLANK: ResumeTemplatePair = { exact: '', fallback: '' }

interface ResumeTemplateState {
  /** Sparse: only customised agents. */
  agents: Record<string, ResumeTemplatePair>
  getTemplates: (agentType: string) => ResumeTemplatePair | undefined
  setTemplate: (agentType: string, field: 'exact' | 'fallback', value: string) => void
  resetAgent: (agentType: string) => void
}

/**
 * Own properties only. An agent type is an open string that reaches this from
 * a daemon payload, so `agents['constructor']` must not resolve up the
 * prototype chain and hand back a function as a template pair.
 */
function own<T>(map: Record<string, T>, key: string): T | undefined {
  return Object.prototype.hasOwnProperty.call(map, key) ? map[key] : undefined
}

function lookup(
  agents: Record<string, ResumeTemplatePair>,
  agentType: string,
): ResumeTemplatePair | undefined {
  return own(agents, agentType) ?? own(DEFAULT_RESUME_TEMPLATES, agentType)
}

export const useResumeTemplateStore = create<ResumeTemplateState>()(
  persist(
    (set, get) => ({
      agents: {},
      getTemplates: (agentType) => lookup(get().agents, agentType),
      setTemplate: (agentType, field, value) => set((s) => {
        // The edit lands on top of whatever currently answers for this agent,
        // so editing one field never silently blanks the other.
        const base = lookup(s.agents, agentType) ?? BLANK
        return { agents: { ...s.agents, [agentType]: { ...base, [field]: value } } }
      }),
      resetAgent: (agentType) => set((s) => {
        if (!own(s.agents, agentType)) return s
        const { [agentType]: _dropped, ...rest } = s.agents
        return { agents: rest }
      }),
    }),
    {
      name: STORAGE_KEYS.RESUME_TEMPLATES,
      storage: purdexStorage,
      version: 1,
      partialize: (state) => ({ agents: state.agents }),
    },
  ),
)

syncManager.register(STORAGE_KEYS.RESUME_TEMPLATES, useResumeTemplateStore)
