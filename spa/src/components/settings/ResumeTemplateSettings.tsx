// spa/src/components/settings/ResumeTemplateSettings.tsx — the per-agent
// resume command templates and their save-time check (spec §4.5).
//
// A separate file rendered inside the Snapshot section: separate so it does not
// deepen issue #975, shared so the templates sit next to the records they
// govern.
//
// Five things live here and nowhere else:
//
//  1. **The probe gets the command word, not the template.** The daemon hands
//     what it receives to the shell as a single positional parameter, so
//     `cld-yolo --resume {id}` would be looked up verbatim and answer
//     `not_found`. Splitting off the first whitespace-separated token — with
//     `{id}` never substituted — is this component's job (spec §4.4).
//  2. **Nothing here can block a save.** The template is written to the store
//     the moment the row commits; every verdict, including "could not check",
//     is advice arriving afterwards.
//  3. **The host picker defaults to the active host.**
//  4. **A 404 is `unverifiable`.** An older daemon has no such endpoint, and
//     that is not the user's problem to debug (spec §8) — as is a network
//     failure, which says nothing about the command either.
//  5. **The limits are on screen** (spec §9): the templates are global while
//     the test is per-host, and the test approximates the pane's shell rather
//     than reproducing it.
//
// A verdict is keyed by `(hostId, commandWord)` and shown only while both still
// match, so a verdict from another machine — or about a word the user has since
// edited — can never sit beside the command being judged now. A response that
// lands after either changed is discarded rather than rendered.
import { useRef, useState } from 'react'
import { ArrowCounterClockwise, CheckCircle, CircleNotch, Question, Warning, XCircle } from '@phosphor-icons/react'
import { AGENT_NAMES } from '../../lib/agent-metadata'
import { resolveShellCommand, type ShellResolveVerdict } from '../../lib/host-api'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useResumeTemplateLookup, useResumeTemplateStore } from '../../stores/useResumeTemplateStore'

type Field = 'exact' | 'fallback'

const FIELDS: readonly Field[] = ['exact', 'fallback']

/** Literal keys, not interpolated ones — they must be greppable in this file. */
const FIELD_LABEL: Record<Field, string> = {
  exact: 'resume_template.field.exact',
  fallback: 'resume_template.field.fallback',
}

/**
 * Every `reason` the daemon can return (spec §4.4). An unknown one is a newer
 * daemon than this build; it still renders as "did not resolve" rather than as
 * a raw token.
 */
const REASON_LABEL: Record<string, string> = {
  not_found: 'resume_template.verdict.not_found',
  shell_metacharacters: 'resume_template.verdict.shell_metacharacters',
  too_long: 'resume_template.verdict.too_long',
  timeout: 'resume_template.verdict.timeout',
  shell_failed: 'resume_template.verdict.shell_failed',
}

/** A verdict, plus the two things it is only true of. */
interface RowResult {
  hostId: string
  commandWord: string
  /** `'pending'` while the request is in flight. */
  verdict: ShellResolveVerdict | 'pending'
}

function rowKey(agentType: string, field: Field): string {
  return `${agentType}:${field}`
}

/**
 * The first whitespace-separated token, `{id}` untouched. This is the only
 * thing that is ever sent to a shell.
 */
function commandWordOf(template: string): string {
  return template.trim().split(/\s+/)[0] ?? ''
}

export function ResumeTemplateSettings({ busy = false }: { busy?: boolean }) {
  const t = useI18nStore((s) => s.t)
  const hosts = useHostStore((s) => s.hosts)
  const hostOrder = useHostStore((s) => s.hostOrder)
  const activeHostId = useHostStore((s) => s.activeHostId)

  const lookup = useResumeTemplateLookup()
  const setTemplate = useResumeTemplateStore((s) => s.setTemplate)
  const resetAgent = useResumeTemplateStore((s) => s.resetAgent)

  // Contract 3: the picker opens on the host the user is working with.
  const [pickedHostId, setPickedHostId] = useState<string>(() => activeHostId ?? hostOrder[0] ?? '')
  // A host can be removed while this is open; fall back rather than probing a
  // host that no longer exists.
  const hostId = hosts[pickedHostId] ? pickedHostId : hostOrder[0] ?? ''

  // Uncommitted edits. Absent means "whatever the store answers", so a reset —
  // or an edit from another window — repaints without any effect syncing state.
  const [drafts, setDrafts] = useState<Record<string, string>>({})
  const [results, setResults] = useState<Record<string, RowResult>>({})

  const liveValue = (agentType: string, field: Field): string => {
    const key = rowKey(agentType, field)
    return drafts[key] ?? lookup(agentType)?.[field] ?? ''
  }

  // What is on screen right now, for the async settle to compare against. A
  // read-only mirror of this render — never a source of truth.
  const liveRef = useRef<{ hostId: string; words: Record<string, string> }>({ hostId, words: {} })
  liveRef.current = {
    hostId,
    words: Object.fromEntries(
      Object.keys(AGENT_NAMES).flatMap((agentType) =>
        FIELDS.map((field) => [rowKey(agentType, field), commandWordOf(liveValue(agentType, field))]),
      ),
    ),
  }

  const dropResult = (key: string) =>
    setResults(({ [key]: _dropped, ...rest }) => rest)

  const handleChange = (agentType: string, field: Field, value: string) => {
    setDrafts((d) => ({ ...d, [rowKey(agentType, field)]: value }))
    // Editing the row invalidates its verdict — including one still in flight.
    dropResult(rowKey(agentType, field))
  }

  const handleCommit = (agentType: string, field: Field, value: string) => {
    setTemplate(agentType, field, value)
  }

  const handleRevert = (agentType: string, field: Field) => {
    const key = rowKey(agentType, field)
    setDrafts(({ [key]: _dropped, ...rest }) => rest)
    dropResult(key)
  }

  const handleHostChange = (nextHostId: string) => {
    setPickedHostId(nextHostId)
    // Contract: a verdict from another machine must never sit beside a command
    // being judged for this one.
    setResults({})
  }

  const handleResetAll = () => {
    for (const agentType of Object.keys(useResumeTemplateStore.getState().agents)) resetAgent(agentType)
    setDrafts({})
    setResults({})
  }

  /** Keep a verdict only while the host and the word it judged still stand. */
  const settle = (key: string, forHost: string, word: string, verdict: ShellResolveVerdict) => {
    const live = liveRef.current
    if (live.hostId !== forHost || live.words[key] !== word) {
      setResults((r) => {
        const current = r[key]
        if (!current || current.hostId !== forHost || current.commandWord !== word) return r
        const { [key]: _dropped, ...rest } = r
        return rest
      })
      return
    }
    setResults((r) => ({ ...r, [key]: { hostId: forHost, commandWord: word, verdict } }))
  }

  const runTest = async (agentType: string, field: Field) => {
    const key = rowKey(agentType, field)
    const word = commandWordOf(liveValue(agentType, field))
    if (!word || !hostId) return
    const forHost = hostId
    setResults((r) => ({ ...r, [key]: { hostId: forHost, commandWord: word, verdict: 'pending' } }))
    try {
      settle(key, forHost, word, await resolveShellCommand(forHost, word))
    } catch {
      // Contract 4's other half: the daemon was unreachable, which says nothing
      // about the command. The template is already saved either way.
      settle(key, forHost, word, { status: 'unverifiable' })
    }
  }

  /** A result is shown only while the pair it was taken for still holds. */
  const shownResult = (agentType: string, field: Field): RowResult | undefined => {
    const key = rowKey(agentType, field)
    const result = results[key]
    if (!result) return undefined
    if (result.hostId !== hostId) return undefined
    if (result.commandWord !== commandWordOf(liveValue(agentType, field))) return undefined
    return result
  }

  return (
    <div data-testid="resume-templates" className="mt-6">
      <h3 className="text-sm text-text-primary">{t('resume_template.title')}</h3>
      <p data-testid="resume-template-limits" className="mt-1 text-xs text-text-secondary">
        {t('resume_template.limit_global')}
        {' '}
        {t('resume_template.limit_probe')}
      </p>

      <label className="mt-3 flex items-center gap-2 text-xs text-text-secondary">
        <span>{t('resume_template.test_against')}</span>
        <select
          data-testid="resume-template-host"
          value={hostId}
          disabled={busy}
          onChange={(e) => handleHostChange(e.target.value)}
          className="rounded border border-border-default bg-bg-input px-2 py-1 text-text-primary disabled:opacity-50"
        >
          {hostOrder.filter((id) => hosts[id]).map((id) => (
            <option key={id} value={id}>{hosts[id].name}</option>
          ))}
        </select>
      </label>

      <div className="mt-3 flex flex-col gap-3">
        {Object.keys(AGENT_NAMES).map((agentType) => (
          <div key={agentType} data-testid={`resume-template-agent-${agentType}`}>
            <div className="text-xs text-text-primary">{AGENT_NAMES[agentType]}</div>
            {FIELDS.map((field) => {
              const value = liveValue(agentType, field)
              const result = shownResult(agentType, field)
              return (
                <TemplateRow
                  key={field}
                  agentType={agentType}
                  field={field}
                  value={value}
                  busy={busy}
                  pending={result?.verdict === 'pending'}
                  result={result}
                  t={t}
                  onChange={handleChange}
                  onCommit={handleCommit}
                  onRevert={handleRevert}
                  onTest={runTest}
                />
              )
            })}
          </div>
        ))}
      </div>

      <button
        type="button"
        data-testid="resume-template-reset"
        onClick={handleResetAll}
        disabled={busy}
        className="mt-3 flex items-center gap-1.5 rounded-md border border-border-default px-3 py-1.5 text-xs text-text-secondary hover:border-border-active hover:text-text-primary disabled:cursor-not-allowed disabled:opacity-50"
      >
        <ArrowCounterClockwise size={14} />
        {t('resume_template.reset_all')}
      </button>
    </div>
  )
}

/**
 * One template field. The three mechanics are `EditableCwdCell`'s (alpha.324),
 * and they are here for the same reasons they are there: `committedRef` so the
 * blur that follows an Enter cannot commit a second time, `disabled` so a row
 * cannot be edited under an action whose result would overwrite it, and
 * `composingRef` + `isComposing` so an IME Enter confirms a candidate instead
 * of committing a half-composed value.
 */
function TemplateRow({
  agentType,
  field,
  value,
  busy,
  pending,
  result,
  t,
  onChange,
  onCommit,
  onRevert,
  onTest,
}: {
  agentType: string
  field: Field
  value: string
  busy: boolean
  pending: boolean
  result?: RowResult
  t: (key: string, params?: Record<string, string | number>) => string
  onChange: (agentType: string, field: Field, value: string) => void
  onCommit: (agentType: string, field: Field, value: string) => void
  onRevert: (agentType: string, field: Field) => void
  onTest: (agentType: string, field: Field) => void
}) {
  const committedRef = useRef(false)
  const composingRef = useRef(false)

  const commit = () => {
    if (committedRef.current) return
    committedRef.current = true
    onCommit(agentType, field, value)
  }

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (composingRef.current || e.nativeEvent.isComposing) return
    if (e.key === 'Enter') {
      e.preventDefault()
      commit()
    } else if (e.key === 'Escape') {
      e.preventDefault()
      // Latch so the trailing blur cannot commit the value being discarded.
      committedRef.current = true
      onRevert(agentType, field)
    }
  }

  const warning = warningFor(field, value)

  return (
    <div className="mt-1 flex flex-wrap items-center gap-2 text-xs">
      <span className="w-28 shrink-0 text-text-secondary">{t(FIELD_LABEL[field])}</span>
      <input
        type="text"
        data-testid={`resume-template-input-${agentType}-${field}`}
        value={value}
        disabled={busy}
        spellCheck={false}
        onChange={(e) => {
          committedRef.current = false
          onChange(agentType, field, e.target.value)
        }}
        onKeyDown={handleKeyDown}
        onCompositionStart={() => { composingRef.current = true }}
        onCompositionEnd={() => { composingRef.current = false }}
        onBlur={commit}
        className="min-w-56 flex-1 rounded border border-border-default bg-bg-input px-2 py-1 font-mono text-text-primary outline-none focus:border-border-active disabled:opacity-50"
      />
      <button
        type="button"
        data-testid={`resume-template-test-${agentType}-${field}`}
        onClick={() => onTest(agentType, field)}
        disabled={busy || pending || !commandWordOf(value)}
        className="rounded-md border border-border-default px-2 py-1 text-text-secondary hover:border-border-active hover:text-text-primary disabled:cursor-not-allowed disabled:opacity-50"
      >
        {t('resume_template.test')}
      </button>
      {result ? <Verdict agentType={agentType} field={field} result={result} t={t} /> : null}
      {warning ? (
        <span
          data-testid={`resume-template-warning-${agentType}-${field}`}
          className="flex w-full items-center gap-1 text-status-warning"
        >
          <Warning size={14} />
          {t(warning)}
        </span>
      ) : null}
    </div>
  )
}

/**
 * The two shapes that still save (spec §4.5): an `exact` without `{id}` resolves
 * to the literal template, and a `{id}` in `fallback` stays literal because
 * there is no id to put there.
 */
function warningFor(field: Field, value: string): string | undefined {
  if (!value.trim()) return undefined
  if (field === 'exact' && !value.includes('{id}')) return 'resume_template.warning.exact_missing_id'
  if (field === 'fallback' && value.includes('{id}')) return 'resume_template.warning.fallback_has_id'
  return undefined
}

function Verdict({
  agentType,
  field,
  result,
  t,
}: {
  agentType: string
  field: Field
  result: RowResult
  t: (key: string, params?: Record<string, string | number>) => string
}) {
  const testId = `resume-template-verdict-${agentType}-${field}`
  const cls = 'flex items-center gap-1'

  if (result.verdict === 'pending') {
    return (
      <span data-testid={testId} data-status="pending" className={`${cls} text-text-secondary`}>
        <CircleNotch size={14} className="animate-spin" />
        {t('resume_template.verdict.pending')}
      </span>
    )
  }
  if (result.verdict.status === 'resolved') {
    return (
      <span data-testid={testId} data-status="resolved" className={`${cls} text-status-success`}>
        <CheckCircle size={14} />
        {t('resume_template.verdict.resolved', { detail: result.verdict.detail })}
      </span>
    )
  }
  if (result.verdict.status === 'unverifiable') {
    return (
      <span data-testid={testId} data-status="unverifiable" className={`${cls} text-text-secondary`}>
        <Question size={14} />
        {t('resume_template.verdict.unverifiable')}
      </span>
    )
  }
  const reason = result.verdict.reason
  return (
    <span
      data-testid={testId}
      data-status="unresolved"
      data-reason={reason}
      className={`${cls} text-status-warning`}
    >
      <XCircle size={14} />
      {t(REASON_LABEL[reason] ?? 'resume_template.verdict.unresolved')}
    </span>
  )
}
