import { useState, useRef, useEffect, useLayoutEffect, useMemo } from 'react'
import { useClickOutside } from '../hooks/useClickOutside'
import { useI18nStore } from '../stores/useI18nStore'
import { isValidSessionName } from '../lib/session-name'
import { EditableValue, type RebuildEditableField } from './RebuildActionSet'
import { collectRenameTargets, type RenameTargetPane } from '../features/workspace/hooks'
import type { Tab } from '../types/tab'

interface Props {
  anchorRect: DOMRect
  currentName: string
  initialValue?: string
  allowUnchangedSubmit?: boolean
  onConfirm: (name: string) => Promise<void>
  onCancel: () => void
  error?: string
  onClearError?: () => void
  placeholder?: string
  validateName?: (trimmedDraft: string, currentName: string) => string | undefined
  /**
   * Tab mode (spec §4.10). When the tab holds at least one terminal
   * `tmux-session` pane, the single rename input is replaced by one detail
   * block per pane — name, working directory, resume command — so the three
   * rebuild fields can be read and corrected on a live session as well as a
   * dead one. Omitted by the editor and storage callers, which keep the
   * single-input popover unchanged.
   */
  tab?: Tab
  /** A live pane's name row: the daemon rename that already exists. */
  onRenamePane?: (target: RenameTargetPane, name: string) => void | Promise<void>
  /** Record-only edits: a dead pane's name, and cwd / resume on any pane. */
  onEditRebuildField?: (target: RenameTargetPane, field: RebuildEditableField, value: string) => void
}

const POPOVER_WIDTH = 240
/** The detail blocks need room for a path and a resume command. */
const PANE_POPOVER_WIDTH = 380
const PADDING = 4

/** Label + value, matching `RebuildActionSet`'s row rhythm minus the checkbox. */
function DetailRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center gap-2 py-0.5">
      <span className="w-28 shrink-0 truncate text-[11px] text-text-secondary">{label}</span>
      <div className="min-w-0 flex-1">{children}</div>
    </div>
  )
}

/**
 * One terminal pane's three rebuild fields.
 *
 * The name row is the only one that can reach the daemon, and only when the
 * session is still there: a terminated pane's name row writes
 * `rebuild.sessionName` instead, because renaming a session that is gone is
 * not a request worth sending. cwd and resume never leave the record.
 */
function PaneDetailBlock({
  target,
  autoFocus,
  onRenamePane,
  onEditRebuildField,
  onClearError,
}: {
  target: RenameTargetPane
  autoFocus: boolean
  onRenamePane?: (target: RenameTargetPane, name: string) => void | Promise<void>
  onEditRebuildField?: (target: RenameTargetPane, field: RebuildEditableField, value: string) => void
  onClearError?: () => void
}) {
  const t = useI18nStore((s) => s.t)
  const live = !target.terminated
  const currentName = live
    ? (target.cachedName || target.sessionCode)
    : (target.record.sessionName || target.cachedName || target.sessionCode)
  const [draft, setDraft] = useState(currentName)
  const [submitting, setSubmitting] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (!autoFocus) return
    inputRef.current?.focus()
    inputRef.current?.select()
  }, [autoFocus])

  const trimmed = draft.trim()
  const invalid = trimmed && trimmed !== currentName && !isValidSessionName(trimmed)
    ? t('tab.rename_invalid_format')
    : undefined

  const commitName = () => {
    if (!trimmed || trimmed === currentName || submitting || invalid) return
    if (!live) {
      onEditRebuildField?.(target, 'sessionName', trimmed)
      return
    }
    setSubmitting(true)
    void Promise.resolve(onRenamePane?.(target, trimmed)).finally(() => setSubmitting(false))
  }

  const handleNameKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    // An IME Enter confirms a candidate; it is not a submit.
    if (e.nativeEvent.isComposing) return
    if (e.key !== 'Enter') return
    // The popover's own Enter handler submits the legacy single-input rename.
    // Each block owns its target, so it must not also fire that.
    e.preventDefault()
    e.stopPropagation()
    commitName()
  }

  return (
    <div data-testid={`rename-pane-block-${target.paneId}`} className="rounded-md border border-border-subtle bg-surface-secondary px-2 py-1.5">
      <div className="flex items-center justify-between gap-2 pb-1">
        <span className="truncate font-mono text-[10px] text-text-muted">{target.sessionCode}</span>
        {!live && (
          <span data-testid={`rename-pane-terminated-${target.paneId}`} className="shrink-0 text-[10px] text-status-warning">
            {t('rebuild.pane_terminated')}
          </span>
        )}
      </div>

      <DetailRow label={t('rebuild.session_name')}>
        <input
          ref={inputRef}
          type="text"
          value={draft}
          onChange={(e) => { setDraft(e.target.value); onClearError?.() }}
          onKeyDown={handleNameKeyDown}
          disabled={submitting}
          placeholder={t('tab.rename_placeholder')}
          className="w-full rounded border border-border-default bg-surface-input px-1.5 py-0.5 font-mono text-xs text-text-primary focus:border-border-active focus:outline-none disabled:opacity-50"
        />
      </DetailRow>

      {/* Enter inside these editors commits the cell, nothing else. */}
      <div onKeyDown={(e) => { if (e.key === 'Enter') e.stopPropagation() }}>
        <DetailRow label={t('rebuild.working_directory')}>
          <EditableValue
            value={target.record.cwd}
            placeholder="—"
            disabled={false}
            testId={`rename-pane-cwd-${target.paneId}`}
            onCommit={(value) => onEditRebuildField?.(target, 'cwd', value)}
          />
        </DetailRow>
        <DetailRow label={t('rebuild.run_resume')}>
          <EditableValue
            value={target.record.resumeCommand}
            placeholder="—"
            disabled={false}
            testId={`rename-pane-resume-${target.paneId}`}
            onCommit={(value) => onEditRebuildField?.(target, 'resumeCommand', value)}
          />
        </DetailRow>
      </div>

      {invalid && <p className="px-1 pt-1 text-[11px] text-red-400">{invalid}</p>}
    </div>
  )
}

export function RenamePopover({ anchorRect, currentName, initialValue, allowUnchangedSubmit = false, onConfirm, onCancel, error, onClearError, placeholder, validateName, tab, onRenamePane, onEditRebuildField }: Props) {
  const t = useI18nStore((s) => s.t)
  const [draft, setDraft] = useState(initialValue ?? currentName)
  const [submitting, setSubmitting] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)
  const containerRef = useRef<HTMLDivElement>(null)

  useClickOutside(containerRef, onCancel)

  const targets = useMemo(() => (tab ? collectRenameTargets(tab) : []), [tab])
  const paneMode = targets.length > 0
  const width = paneMode ? PANE_POPOVER_WIDTH : POPOVER_WIDTH

  const trimmedDraft = draft.trim()
  const validationError = validateName
    ? validateName(trimmedDraft, currentName)
    : (trimmedDraft && trimmedDraft !== currentName && !isValidSessionName(trimmedDraft)
        ? t('tab.rename_invalid_format')
        : undefined)
  const displayError = paneMode ? error : (validationError ?? error)

  // Focus + select all on mount
  useEffect(() => {
    const input = inputRef.current
    if (input) {
      input.focus()
      input.select()
    }
  }, [])

  // Position: centered below anchor, clamped to viewport
  useLayoutEffect(() => {
    const el = containerRef.current
    if (!el) return
    // Horizontal clamping (existing)
    let left = anchorRect.left + anchorRect.width / 2 - width / 2
    left = Math.max(PADDING, Math.min(left, window.innerWidth - width - PADDING))
    // Vertical clamping
    const popoverHeight = el.offsetHeight
    let top = anchorRect.bottom + PADDING
    if (top + popoverHeight > window.innerHeight - PADDING) {
      top = anchorRect.top - PADDING - popoverHeight
    }
    if (top < PADDING) {
      top = PADDING
    }
    el.style.left = `${left}px`
    el.style.top = `${top}px`
  }, [anchorRect, displayError, width, targets.length])

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.preventDefault()
      onCancel()
    }
    if (e.key === 'Enter') {
      e.preventDefault()
      const trimmed = draft.trim()
      if (!trimmed || (!allowUnchangedSubmit && trimmed === currentName) || submitting || validationError) return
      setSubmitting(true)
      onConfirm(trimmed).finally(() => setSubmitting(false))
    }
  }

  return (
    <div
      ref={containerRef}
      onKeyDown={handleKeyDown}
      className="fixed z-50 bg-surface-elevated border border-border-default rounded-lg shadow-xl p-2"
      style={{ width }}
    >
      {paneMode
        ? (
          <div className="space-y-2">
            {targets.map((target, i) => (
              <PaneDetailBlock
                key={target.paneId}
                target={target}
                autoFocus={i === 0}
                onRenamePane={onRenamePane}
                onEditRebuildField={onEditRebuildField}
                onClearError={onClearError}
              />
            ))}
          </div>
        )
        : (
          <input
            ref={inputRef}
            type="text"
            value={draft}
            onChange={(e) => { setDraft(e.target.value); onClearError?.() }}
            disabled={submitting}
            placeholder={placeholder ?? t('tab.rename_placeholder')}
            className="w-full bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-3 py-1.5 focus:border-border-active focus:outline-none disabled:opacity-50"
          />
        )}
      {displayError && (
        <p className="text-xs text-red-400 mt-1 px-1">{displayError}</p>
      )}
    </div>
  )
}
