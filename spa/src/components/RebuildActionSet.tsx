// spa/src/components/RebuildActionSet.tsx — the three editable rebuild rows
// and their footer (spec §4.7, §4.8, §4.9, §7, §9.1).
//
// Presentational on purpose: everything it shows about the pane arrives as
// props, so Task 15's tab-name popover can mount one block per pane without
// the component reaching into the tab store on its own. The one store it does
// read is `useRebuildStore`, because the operation report is keyed by paneId
// there precisely so it survives the remount that clearing `terminated`
// causes (spec §4.8) — component state could not.
import { useEffect, useRef, useState } from 'react'
import { useI18nStore } from '../stores/useI18nStore'
import { useRebuildStore } from '../stores/useRebuildStore'
import { retryResume, attachAnyway, type RebuildPlan, type StepResult } from '../lib/rebuild/engine'
import { AGENT_NAMES } from '../lib/agent-metadata'
import type { PaneRebuildRecord, TerminatedReason } from '../types/tab'

/** The record fields the user may hand-edit (`RebuildPatch` kind `'field'`). */
export type RebuildEditableField = 'sessionName' | 'cwd' | 'resumeCommand'

/**
 * What the panel needs out of a rebuild operation. Structurally satisfied by
 * `RebuildOperation`, and loose enough that a caller (or a test) can hand in
 * just the report.
 */
export interface RebuildOperationView {
  status?: 'running' | 'done'
  report?: {
    hostId?: string
    created?: { code: string; name: string; tmuxInstance?: string }
    steps?: { create?: StepResult; resume?: StepResult; repoint?: StepResult }
    repointed?: boolean
  }
}

interface Props {
  tabId: string
  paneId: string
  record: PaneRebuildRecord
  /** Set on a dead pane; drives the create-row lock and the host-removed copy. */
  terminated?: TerminatedReason
  /** Overrides the store lookup — used by the popover and by tests. */
  operation?: RebuildOperationView
  onRebuild: (plan: RebuildPlan) => void
  onEdit?: (field: RebuildEditableField, value: string) => void
  onRetryResume?: () => void
  onAttachAnyway?: () => void
}

const LIMIT_KEYS = ['agent_only', 'minimal_flags', 'cwd_scoped', 'multi_pane', 'local_storage'] as const

/**
 * Double-click-to-edit cell, carrying `EditableCwdCell`'s two review findings
 * verbatim (alpha.324):
 *
 * - `committedRef` latches the moment an edit resolves, so the trailing blur
 *   that follows an Enter commit — or an Escape cancel — cannot commit again.
 * - `composingRef` + `e.nativeEvent.isComposing` let an IME's Enter confirm a
 *   CJK candidate instead of committing a half-composed value.
 *
 * `disabled` blocks entering edit mode at all, which is what freezes the rows
 * once an operation has created a session.
 *
 * Exported because Task 15's tab-name popover shows the same cwd and resume
 * rows on a live pane, and those guards are not worth rediscovering twice.
 */
export function EditableValue({
  value,
  placeholder,
  disabled,
  testId,
  onCommit,
}: {
  value?: string
  placeholder: string
  disabled: boolean
  testId: string
  onCommit: (next: string) => void
}) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState('')
  const committedRef = useRef(false)
  const composingRef = useRef(false)
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (editing && inputRef.current) {
      inputRef.current.focus()
      inputRef.current.select()
    }
  }, [editing])

  const startEditing = () => {
    if (disabled) return
    setDraft(value ?? '')
    committedRef.current = false
    setEditing(true)
  }

  const commit = () => {
    if (committedRef.current) return
    committedRef.current = true
    setEditing(false)
    onCommit(draft.trim())
  }

  const cancel = () => {
    committedRef.current = true
    setEditing(false)
  }

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    // The keystroke belongs to the IME (confirm/cancel candidate), not the edit.
    if (composingRef.current || e.nativeEvent.isComposing) return
    if (e.key === 'Enter') {
      e.preventDefault()
      commit()
    } else if (e.key === 'Escape') {
      e.preventDefault()
      cancel()
    }
  }

  if (editing) {
    return (
      <input
        ref={inputRef}
        data-testid={`${testId}-input`}
        type="text"
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={handleKeyDown}
        onCompositionStart={() => { composingRef.current = true }}
        onCompositionEnd={() => { composingRef.current = false }}
        onBlur={commit}
        className="w-full rounded border border-border-active bg-surface-input px-1.5 py-0.5 font-mono text-xs text-text-primary outline-none"
      />
    )
  }

  return (
    <span
      data-testid={`${testId}-cell`}
      onDoubleClick={startEditing}
      className={`block truncate rounded px-1.5 py-0.5 font-mono text-xs ${
        disabled ? 'text-text-muted' : 'cursor-text text-text-secondary hover:bg-surface-hover hover:text-text-primary'
      }`}
    >
      {value || placeholder}
    </span>
  )
}

/** One checkbox + label + value cell. The value slot is whatever the row shows. */
function ActionRow({
  id,
  label,
  checked,
  disabled,
  onToggle,
  children,
}: {
  id: string
  label: string
  checked: boolean
  disabled: boolean
  onToggle: () => void
  children: React.ReactNode
}) {
  return (
    <div className="flex items-center gap-2 py-1">
      <input
        id={id}
        type="checkbox"
        checked={checked}
        disabled={disabled}
        onChange={onToggle}
        className="shrink-0 accent-accent"
      />
      <label htmlFor={id} className="w-40 shrink-0 truncate text-xs text-text-secondary">{label}</label>
      <div className="min-w-0 flex-1">{children}</div>
    </div>
  )
}

export function RebuildActionSet({
  tabId,
  paneId,
  record,
  terminated,
  operation,
  onRebuild,
  onEdit,
  onRetryResume,
  onAttachAnyway,
}: Props) {
  const t = useI18nStore((s) => s.t)
  const storedOperation = useRebuildStore((s) => s.operations[paneId])
  const op: RebuildOperationView | undefined = operation ?? storedOperation

  // Per-row overrides on top of the derived defaults, so a value that arrives
  // later (a cwd the user just typed in) turns its row back on by itself.
  const [override, setOverride] = useState<Partial<RebuildPlan>>({})

  const hostRemoved = terminated === 'host-removed'
  const busy = op?.status === 'running'
  const created = op?.report?.created
  const resumeStep = op?.report?.steps?.resume
  const repointed = op?.report?.repointed ?? false
  // Once a session exists the rows describe something already done; editing
  // them would only pretend to change it.
  const frozen = busy || !!created || hostRemoved

  const hasCwd = !!record.cwd
  const hasResume = !!record.resumeCommand
  // A dead pane's session has to be recreated — unchecking is only meaningful
  // when re-pointing at something live, which the session picker does (§4.9).
  const createLocked = !!terminated
  const plan: RebuildPlan = {
    createSession: createLocked ? true : (override.createSession ?? true),
    applyCwd: hasCwd && (override.applyCwd ?? true),
    // An unverified record still shows its exact command, but off by default (§9.1).
    runResume: hasResume && (override.runResume ?? !record.unverified),
  }

  const rowId = (row: string) => `rebuild-${tabId}-${paneId}-${row}`
  const edit = (field: RebuildEditableField) => (value: string) => onEdit?.(field, value)
  const agentLabel = record.agent ? (AGENT_NAMES[record.agent.type] ?? record.agent.type) : ''

  return (
    <div data-testid="rebuild-action-set" className="w-full rounded-md border border-border-subtle bg-surface-secondary p-3 text-left">
      <ActionRow
        id={rowId('create')}
        label={t('rebuild.create_session')}
        checked={plan.createSession}
        disabled={frozen || createLocked}
        onToggle={() => setOverride((o) => ({ ...o, createSession: !plan.createSession }))}
      >
        {created
          ? (
            <span data-testid="rebuild-created-name" className="block truncate px-1.5 py-0.5 font-mono text-xs text-text-primary">
              {created.name}
            </span>
          )
          : (
            <EditableValue
              value={record.sessionName}
              placeholder="—"
              disabled={frozen}
              testId="rebuild-session-name"
              onCommit={edit('sessionName')}
            />
          )}
      </ActionRow>

      <ActionRow
        id={rowId('cwd')}
        label={t('rebuild.working_directory')}
        checked={plan.applyCwd}
        disabled={frozen || !hasCwd}
        onToggle={() => setOverride((o) => ({ ...o, applyCwd: !plan.applyCwd }))}
      >
        <EditableValue
          value={record.cwd}
          placeholder="—"
          disabled={frozen}
          testId="rebuild-cwd"
          onCommit={edit('cwd')}
        />
      </ActionRow>

      <ActionRow
        id={rowId('resume')}
        label={t('rebuild.run_resume')}
        checked={plan.runResume}
        disabled={frozen || !hasResume}
        onToggle={() => setOverride((o) => ({ ...o, runResume: !plan.runResume }))}
      >
        <EditableValue
          value={record.resumeCommand}
          placeholder="—"
          disabled={frozen}
          testId="rebuild-resume-command"
          onCommit={edit('resumeCommand')}
        />
      </ActionRow>

      {agentLabel && (
        <p className="pl-[1.65rem] text-[11px] text-text-muted">{agentLabel}</p>
      )}
      {!hasResume && (
        <p data-testid="rebuild-no-agent-hint" className="pl-[1.65rem] pt-1 text-[11px] text-text-muted">
          {t('rebuild.no_agent_hint')}
        </p>
      )}
      {record.unverified && (
        <p data-testid="rebuild-unverified-hint" className="pl-[1.65rem] pt-1 text-[11px] text-status-warning">
          {t('rebuild.unverified_hint')}
        </p>
      )}

      {created && (
        <p className="pt-2 text-[11px] text-text-muted">
          <span className="font-mono">{created.code}</span>
        </p>
      )}
      {resumeStep?.status === 'failed' && resumeStep.error && (
        <p data-testid="rebuild-error" className="pt-1 text-[11px] text-status-error">{resumeStep.error}</p>
      )}

      <div className="flex justify-end gap-2 pt-3">
        {hostRemoved
          ? (
            <p data-testid="rebuild-host-removed-hint" className="text-[11px] text-text-muted">
              {t('rebuild.host_removed_hint')}
            </p>
          )
          : created
            ? (
              <>
                {resumeStep?.status === 'failed' && (
                  <button
                    type="button"
                    disabled={busy}
                    onClick={() => (onRetryResume ? onRetryResume() : void retryResume(paneId))}
                    className="rounded border border-border-default px-2.5 py-1 text-xs text-text-secondary hover:text-text-primary disabled:opacity-50"
                  >
                    {t('rebuild.retry_resume')}
                  </button>
                )}
                {!repointed && (
                  <button
                    type="button"
                    disabled={busy}
                    onClick={() => (onAttachAnyway ? onAttachAnyway() : void attachAnyway(paneId))}
                    className="rounded bg-accent px-2.5 py-1 text-xs text-text-inverse hover:bg-accent-hover disabled:opacity-50"
                  >
                    {t('rebuild.attach_anyway')}
                  </button>
                )}
              </>
            )
            : (
              <button
                type="button"
                disabled={busy}
                onClick={() => onRebuild(plan)}
                className="rounded bg-accent px-2.5 py-1 text-xs text-text-inverse hover:bg-accent-hover disabled:opacity-50"
              >
                {t('rebuild.button')}
              </button>
            )}
      </div>

      <details className="pt-2">
        <summary className="cursor-pointer text-[11px] text-text-muted">{t('rebuild.limits_title')}</summary>
        <ul className="list-disc space-y-1 pt-1 pl-4 text-[11px] text-text-muted">
          {LIMIT_KEYS.map((key) => (
            <li key={key} data-testid={`rebuild-limit-${key}`}>{t(`rebuild.limits_${key}`)}</li>
          ))}
        </ul>
      </details>
    </div>
  )
}
