import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { RebuildActionSet } from './RebuildActionSet'
import { useRebuildStore } from '../stores/useRebuildStore'
import { useResumeTemplateStore } from '../stores/useResumeTemplateStore'
import type { PaneRebuildRecord } from '../types/tab'

const record: PaneRebuildRecord = {
  sessionName: 'dev', tmuxInstance: '111:1000', cwd: '/w/p',
  agent: { type: 'cc', sessionId: 'S1', updatedAt: 1 },
  capturedAt: 1,
}

beforeEach(() => {
  useRebuildStore.setState({ operations: {}, lockedBy: null })
  useResumeTemplateStore.setState({ agents: {} })
})

// The panel shows what the next Rebuild would send, and that string is
// RESOLVED, not stored (spec §4.2). Reading it through an unsubscribed path
// would satisfy every "the consumer calls the resolver" assertion and still
// leave a stale command on screen after the user edits the template — so the
// re-render is asserted directly, with no remount.
describe('RebuildActionSet — the resume row is resolved live', () => {
  it('renders the agent template composed with the recorded session id', () => {
    render(<RebuildActionSet tabId="t1" paneId="p1" record={record} onRebuild={vi.fn()} />)
    expect(screen.getByTestId('rebuild-resume-command-cell')).toHaveTextContent('claude --resume S1')
  })

  it('renders the pane override ahead of the template', () => {
    render(<RebuildActionSet tabId="t1" paneId="p1" onRebuild={vi.fn()}
      record={{ ...record, resumeCommandOverride: 'cld-yolo -c' }} />)
    expect(screen.getByTestId('rebuild-resume-command-cell')).toHaveTextContent('cld-yolo -c')
  })

  it('re-renders when the template changes, without a remount', () => {
    render(<RebuildActionSet tabId="t1" paneId="p1" record={record} onRebuild={vi.fn()} />)
    expect(screen.getByTestId('rebuild-resume-command-cell')).toHaveTextContent('claude --resume S1')
    act(() => { useResumeTemplateStore.getState().setTemplate('cc', 'exact', 'cld-yolo --resume {id}') })
    expect(screen.getByTestId('rebuild-resume-command-cell')).toHaveTextContent('cld-yolo --resume S1')
  })

  it('turns the row back on when a template appears for an unknown agent', () => {
    render(<RebuildActionSet tabId="t1" paneId="p1" onRebuild={vi.fn()}
      record={{ ...record, agent: { type: 'aider', sessionId: 'S1', updatedAt: 1 } }} />)
    expect(screen.getByRole('checkbox', { name: /resume/i })).toBeDisabled()
    act(() => { useResumeTemplateStore.getState().setTemplate('aider', 'exact', 'aider --restore {id}') })
    expect(screen.getByRole('checkbox', { name: /resume/i })).toBeEnabled()
    expect(screen.getByTestId('rebuild-resume-command-cell')).toHaveTextContent('aider --restore S1')
  })
})

// Spec §4.3 — the panel pins the command an operation is carrying only while
// that operation is still actionable. "Retry resume" acts on a session that
// already exists, so it must show the string the engine pinned at operation
// start; but an operation that failed before creating anything is not
// actionable, and the next Rebuild recomputes from scratch — so the panel goes
// back to the live resolution and a template edited meanwhile is visible
// BEFORE the button is pressed.
describe('RebuildActionSet — the displayed command is pinned only while actionable', () => {
  // The real operation shape: `resumeCommand` at the top level, `created`
  // nested under `report`. A fixture that invented a top-level `created` would
  // let an implementation reading the wrong field pass.
  const runningOp = {
    status: 'running' as const,
    resumeCommand: 'claude --resume PINNED',
    report: { steps: { create: { status: 'ok' as const } } },
  }

  it('shows the live resolution when there is no operation at all', () => {
    render(<RebuildActionSet tabId="t1" paneId="p1" record={record} onRebuild={vi.fn()} />)
    expect(screen.getByTestId('rebuild-resume-command-cell')).toHaveTextContent('claude --resume S1')
  })

  it('pins the operation string while the operation is running, through a template change', () => {
    render(<RebuildActionSet tabId="t1" paneId="p1" record={record} onRebuild={vi.fn()} operation={runningOp} />)
    expect(screen.getByTestId('rebuild-resume-command-cell')).toHaveTextContent('claude --resume PINNED')
    // Another window edits the template mid-flight. What is in flight does not change.
    act(() => { useResumeTemplateStore.getState().setTemplate('cc', 'exact', 'cld-yolo --resume {id}') })
    expect(screen.getByTestId('rebuild-resume-command-cell')).toHaveTextContent('claude --resume PINNED')
    expect(screen.getByTestId('rebuild-resume-command-cell')).not.toHaveTextContent('cld-yolo')
  })

  it('pins the operation string once a session exists, even after the operation finished', () => {
    useRebuildStore.setState({
      operations: {
        p1: {
          paneId: 'p1', tabId: 't1', hostId: 'h1',
          plan: { createSession: true, applyCwd: true, runResume: true },
          binding: { hostId: 'h1', sessionCode: 'old1', tmuxInstance: '111:1000' },
          resumeCommand: 'claude --resume PINNED',
          createdSession: {
            code: 'new1', name: 'dev', cwd: '/w/p', mode: 'terminal',
            cc_session_id: '', cc_model: '', has_relay: false, tmux_instance: '222:2000',
          },
          status: 'done',
          report: {
            hostId: 'h1',
            created: { code: 'new1', name: 'dev', tmuxInstance: '222:2000' },
            steps: { create: { status: 'ok' }, resume: { status: 'failed', error: 'send-keys failed: 500' }, repoint: { status: 'skipped' } },
            repointed: false,
          },
          startedAt: 1,
        },
      },
    })
    render(<RebuildActionSet tabId="t1" paneId="p1" record={record} onRebuild={vi.fn()} />)
    // Retry resume re-sends against the created session: it must show what it will send.
    expect(screen.getByRole('button', { name: /retry resume/i })).toBeEnabled()
    expect(screen.getByTestId('rebuild-resume-command-cell')).toHaveTextContent('claude --resume PINNED')
    act(() => { useResumeTemplateStore.getState().setTemplate('cc', 'exact', 'cld-yolo --resume {id}') })
    expect(screen.getByTestId('rebuild-resume-command-cell')).toHaveTextContent('claude --resume PINNED')
  })

  it('returns to the live resolution when the operation finished without creating anything', () => {
    render(<RebuildActionSet tabId="t1" paneId="p1" record={record} onRebuild={vi.fn()}
      operation={{
        status: 'done',
        resumeCommand: 'claude --resume PINNED',
        report: {
          hostId: 'h1', repointed: false,
          steps: {
            create: { status: 'failed', error: 'host h1 is not configured' },
            resume: { status: 'skipped' }, repoint: { status: 'skipped' },
          },
        },
      }} />)
    expect(screen.getByTestId('rebuild-resume-command-cell')).toHaveTextContent('claude --resume S1')
    expect(screen.getByTestId('rebuild-resume-command-cell')).not.toHaveTextContent('PINNED')
  })

  it('shows a template edited after a failed create, and rebuilds with the resume row on', () => {
    const onRebuild = vi.fn()
    render(<RebuildActionSet tabId="t1" paneId="p1" record={record} onRebuild={onRebuild}
      operation={{
        status: 'done',
        resumeCommand: 'claude --resume PINNED',
        report: {
          hostId: 'h1', repointed: false,
          steps: {
            create: { status: 'failed', error: 'host h1 is not configured' },
            resume: { status: 'skipped' }, repoint: { status: 'skipped' },
          },
        },
      }} />)
    act(() => { useResumeTemplateStore.getState().setTemplate('cc', 'exact', 'cld-yolo --resume {id}') })
    expect(screen.getByTestId('rebuild-resume-command-cell')).toHaveTextContent('cld-yolo --resume S1')
    // And the button the user presses next is the one that would send it.
    const button = screen.getByRole('button', { name: /^rebuild$/i })
    expect(button).toBeEnabled()
    fireEvent.click(button)
    expect(onRebuild).toHaveBeenCalledWith(expect.objectContaining({ runResume: true }))
  })
})

describe('RebuildActionSet', () => {
  it('checks all three rows by default', () => {
    render(<RebuildActionSet tabId="t1" paneId="p1" record={record} onRebuild={vi.fn()} />)
    const boxes = screen.getAllByRole('checkbox')
    expect(boxes).toHaveLength(3)
    boxes.forEach((cb) => expect(cb).toBeChecked())
  })

  it('disables and unchecks the resume row when there is no command', () => {
    render(<RebuildActionSet tabId="t1" paneId="p1" record={{ ...record, resumeCommandOverride: undefined, agent: undefined }} onRebuild={vi.fn()} />)
    const resume = screen.getByRole('checkbox', { name: /resume/i })
    expect(resume).toBeDisabled()
    expect(resume).not.toBeChecked()
  })

  it('passes the unchecked rows through to the plan', () => {
    const onRebuild = vi.fn()
    render(<RebuildActionSet tabId="t1" paneId="p1" record={record} onRebuild={onRebuild} />)
    fireEvent.click(screen.getByRole('checkbox', { name: /resume/i }))
    fireEvent.click(screen.getByRole('button', { name: /rebuild/i }))
    expect(onRebuild).toHaveBeenCalledWith({ createSession: true, applyCwd: true, runResume: false })
  })

  it('does not commit an edit on an IME Enter', () => {
    const onEdit = vi.fn()
    render(<RebuildActionSet tabId="t1" paneId="p1" record={record} onRebuild={vi.fn()} onEdit={onEdit} />)
    fireEvent.doubleClick(screen.getByText('/w/p'))
    const input = screen.getByDisplayValue('/w/p')
    fireEvent.change(input, { target: { value: '/w/other' } })
    fireEvent.keyDown(input, { key: 'Enter', isComposing: true })
    expect(onEdit).not.toHaveBeenCalled()
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(onEdit).toHaveBeenCalledWith('cwd', '/w/other')
  })

  it('shows retry actions after a failed resume instead of a fresh rebuild', () => {
    render(<RebuildActionSet tabId="t1" paneId="p1" record={record} onRebuild={vi.fn()}
      operation={{ report: { hostId: 'h1', created: { code: 'new1', name: 'dev', tmuxInstance: '222:2000' },
        steps: { create: { status: 'ok' }, resume: { status: 'failed', error: 'send-keys failed: 500' }, repoint: { status: 'skipped' } } } }} />)
    expect(screen.getByRole('button', { name: /retry resume/i })).toBeEnabled()
    expect(screen.getByRole('button', { name: /attach anyway/i })).toBeEnabled()
    expect(screen.getByText(/dev/)).toBeInTheDocument()
  })

  // === Row rules (plan Task 13 table) ===

  it('a terminated pane cannot uncheck "Create tmux session"', () => {
    const onRebuild = vi.fn()
    render(<RebuildActionSet tabId="t1" paneId="p1" record={record} terminated="tmux-restarted" onRebuild={onRebuild} />)
    const create = screen.getByRole('checkbox', { name: /create tmux session/i })
    expect(create).toBeChecked()
    expect(create).toBeDisabled()
    fireEvent.click(create)
    expect(create).toBeChecked()
    fireEvent.click(screen.getByRole('button', { name: /^rebuild$/i }))
    expect(onRebuild).toHaveBeenCalledWith({ createSession: true, applyCwd: true, runResume: true })
  })

  it('disables and unchecks the cwd row when no directory was recorded', () => {
    const onRebuild = vi.fn()
    render(<RebuildActionSet tabId="t1" paneId="p1" record={{ ...record, cwd: undefined }} onRebuild={onRebuild} />)
    const cwd = screen.getByRole('checkbox', { name: /working directory/i })
    expect(cwd).toBeDisabled()
    expect(cwd).not.toBeChecked()
    fireEvent.click(screen.getByRole('button', { name: /^rebuild$/i }))
    expect(onRebuild).toHaveBeenCalledWith({ createSession: true, applyCwd: false, runResume: true })
  })

  it('explains that a pane with no agent rebuilds as a shell', () => {
    render(<RebuildActionSet tabId="t1" paneId="p1" record={{ ...record, resumeCommandOverride: undefined, agent: undefined }} onRebuild={vi.fn()} />)
    expect(screen.getByTestId('rebuild-no-agent-hint')).toBeInTheDocument()
    expect(screen.queryByTestId('rebuild-unverified-hint')).toBeNull()
  })

  it('shows an unverified resume row unchecked, with its hint, but still runnable', () => {
    const onRebuild = vi.fn()
    render(<RebuildActionSet tabId="t1" paneId="p1" record={{ ...record, unverified: true }} onRebuild={onRebuild} />)
    const resume = screen.getByRole('checkbox', { name: /resume/i })
    expect(resume).toBeEnabled()
    expect(resume).not.toBeChecked()
    expect(screen.getByTestId('rebuild-unverified-hint')).toBeInTheDocument()
    expect(screen.getByText('claude --resume S1')).toBeInTheDocument()

    fireEvent.click(resume)
    expect(resume).toBeChecked()
    fireEvent.click(screen.getByRole('button', { name: /^rebuild$/i }))
    expect(onRebuild).toHaveBeenCalledWith({ createSession: true, applyCwd: true, runResume: true })
  })

  it('hides Rebuild and explains itself when the host was removed', () => {
    render(<RebuildActionSet tabId="t1" paneId="p1" record={record} terminated="host-removed" onRebuild={vi.fn()} />)
    expect(screen.queryByRole('button', { name: /^rebuild$/i })).toBeNull()
    expect(screen.getByTestId('rebuild-host-removed-hint')).toBeInTheDocument()
  })

  // === Editing ===

  it('commits session name and resume command edits through onEdit', () => {
    const onEdit = vi.fn()
    render(<RebuildActionSet tabId="t1" paneId="p1" record={record} onRebuild={vi.fn()} onEdit={onEdit} />)

    fireEvent.doubleClick(screen.getByText('dev'))
    const nameInput = screen.getByDisplayValue('dev')
    fireEvent.change(nameInput, { target: { value: ' purdex1 ' } })
    fireEvent.keyDown(nameInput, { key: 'Enter' })
    expect(onEdit).toHaveBeenCalledWith('sessionName', 'purdex1')

    fireEvent.doubleClick(screen.getByText('claude --resume S1'))
    const cmdInput = screen.getByDisplayValue('claude --resume S1')
    fireEvent.change(cmdInput, { target: { value: 'claude -c' } })
    fireEvent.keyDown(cmdInput, { key: 'Enter' })
    expect(onEdit).toHaveBeenCalledWith('resumeCommandOverride', 'claude -c')
  })

  it('commits an edit only once when Enter is followed by the trailing blur', () => {
    const onEdit = vi.fn()
    render(<RebuildActionSet tabId="t1" paneId="p1" record={record} onRebuild={vi.fn()} onEdit={onEdit} />)
    fireEvent.doubleClick(screen.getByText('/w/p'))
    const input = screen.getByDisplayValue('/w/p')
    fireEvent.change(input, { target: { value: '/w/other' } })
    fireEvent.keyDown(input, { key: 'Enter' })
    fireEvent.blur(input)
    expect(onEdit).toHaveBeenCalledTimes(1)
  })

  it('Escape cancels the edit without committing', () => {
    const onEdit = vi.fn()
    render(<RebuildActionSet tabId="t1" paneId="p1" record={record} onRebuild={vi.fn()} onEdit={onEdit} />)
    fireEvent.doubleClick(screen.getByText('/w/p'))
    const input = screen.getByDisplayValue('/w/p')
    fireEvent.change(input, { target: { value: '/w/other' } })
    fireEvent.keyDown(input, { key: 'Escape' })
    fireEvent.blur(input)
    expect(onEdit).not.toHaveBeenCalled()
    expect(screen.getByText('/w/p')).toBeInTheDocument()
  })

  it('lets the user supply a directory the capture never recorded', () => {
    const onEdit = vi.fn()
    render(<RebuildActionSet tabId="t1" paneId="p1" record={{ ...record, cwd: undefined }} onRebuild={vi.fn()} onEdit={onEdit} />)
    fireEvent.doubleClick(screen.getByTestId('rebuild-cwd-cell'))
    const input = screen.getByTestId('rebuild-cwd-input')
    fireEvent.change(input, { target: { value: '/w/typed' } })
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(onEdit).toHaveBeenCalledWith('cwd', '/w/typed')
  })

  it('freezes the rows once a session exists, so an edit cannot pretend to change it', () => {
    render(<RebuildActionSet tabId="t1" paneId="p1" record={record} onRebuild={vi.fn()}
      operation={{ report: { created: { code: 'new1', name: 'dev' }, steps: { create: { status: 'ok' }, resume: { status: 'failed', error: 'boom' } } } }} />)
    screen.getAllByRole('checkbox').forEach((cb) => expect(cb).toBeDisabled())
    fireEvent.doubleClick(screen.getByTestId('rebuild-cwd-cell'))
    expect(screen.queryByTestId('rebuild-cwd-input')).toBeNull()
  })

  // === The report is store-backed, so it survives a remount ===

  it('reads the operation from useRebuildStore when no prop is given', () => {
    useRebuildStore.setState({
      operations: {
        p1: {
          paneId: 'p1', tabId: 't1', hostId: 'h1',
          plan: { createSession: true, applyCwd: true, runResume: true },
          binding: { hostId: 'h1', sessionCode: 'old1', tmuxInstance: '111:1000' },
          resumeCommand: 'claude --resume S1',
          createdSession: {
            code: 'new1', name: 'dev', cwd: '/w/p', mode: 'terminal',
            cc_session_id: '', cc_model: '', has_relay: false, tmux_instance: '222:2000',
          },
          status: 'done',
          report: {
            hostId: 'h1',
            created: { code: 'new1', name: 'dev', tmuxInstance: '222:2000' },
            steps: { create: { status: 'ok' }, resume: { status: 'failed', error: 'send-keys failed: 500' }, repoint: { status: 'skipped' } },
            repointed: false,
          },
          startedAt: 1,
        },
      },
    })
    render(<RebuildActionSet tabId="t1" paneId="p1" record={record} onRebuild={vi.fn()} />)
    expect(screen.getByRole('button', { name: /retry resume/i })).toBeEnabled()
    expect(screen.getByText(/send-keys failed: 500/)).toBeInTheDocument()
  })

  it('offers only "Attach anyway" when the resume worked but the re-point did not', () => {
    render(<RebuildActionSet tabId="t1" paneId="p1" record={record} onRebuild={vi.fn()}
      operation={{ report: { created: { code: 'new1', name: 'dev' }, repointed: false,
        steps: { create: { status: 'ok' }, resume: { status: 'ok' }, repoint: { status: 'skipped', error: 'the pane binding changed' } } } }} />)
    expect(screen.queryByRole('button', { name: /retry resume/i })).toBeNull()
    expect(screen.getByRole('button', { name: /attach anyway/i })).toBeEnabled()
  })

  it('offers nothing to press once the daemon refused on the generation', () => {
    // The refusal is not a failure to work around: the code the rebuild
    // created belongs to another tmux generation now, so neither re-sending
    // nor attaching can be right (spec §4.6.2).
    render(<RebuildActionSet tabId="t1" paneId="p1" record={record} onRebuild={vi.fn()}
      operation={{ report: { created: { code: 'new1', name: 'dev' }, repointed: false,
        steps: {
          create: { status: 'ok' },
          resume: { status: 'failed', error: 'session new1 belongs to another tmux generation', refusal: 'generation' },
          repoint: { status: 'failed', error: 'session new1 belongs to another tmux generation' },
        } } }} />)
    expect(screen.queryByRole('button', { name: /retry resume/i })).toBeNull()
    expect(screen.queryByRole('button', { name: /attach anyway/i })).toBeNull()
    expect(screen.getByTestId('rebuild-generation-refused-hint')).toBeInTheDocument()
  })

  it('calls the retry callbacks it was given', () => {
    const onRetryResume = vi.fn()
    const onAttachAnyway = vi.fn()
    render(<RebuildActionSet tabId="t1" paneId="p1" record={record} onRebuild={vi.fn()}
      onRetryResume={onRetryResume} onAttachAnyway={onAttachAnyway}
      operation={{ report: { created: { code: 'new1', name: 'dev' },
        steps: { create: { status: 'ok' }, resume: { status: 'failed', error: 'boom' } } } }} />)
    fireEvent.click(screen.getByRole('button', { name: /retry resume/i }))
    fireEvent.click(screen.getByRole('button', { name: /attach anyway/i }))
    expect(onRetryResume).toHaveBeenCalledTimes(1)
    expect(onAttachAnyway).toHaveBeenCalledTimes(1)
  })

  it('disables the actions while the operation is running', () => {
    render(<RebuildActionSet tabId="t1" paneId="p1" record={record} onRebuild={vi.fn()}
      operation={{ status: 'running', report: { steps: { create: { status: 'skipped' } } } }} />)
    expect(screen.getByRole('button', { name: /^rebuild$/i })).toBeDisabled()
    screen.getAllByRole('checkbox').forEach((cb) => expect(cb).toBeDisabled())
  })

  // === Failure reporting ===
  //
  // An invalid session name, an offline host or an exhausted rename retry all
  // land in `steps.create`. Rendering only the resume error left Rebuild
  // restoring its button with nothing said about why it did nothing.

  it('shows why the create failed, with Rebuild still available', () => {
    render(<RebuildActionSet tabId="t1" paneId="p1" record={record} onRebuild={vi.fn()}
      operation={{ status: 'done', report: { hostId: 'h1', repointed: false, steps: {
        create: { status: 'failed', error: 'host h1 is not configured' },
        resume: { status: 'skipped' }, repoint: { status: 'skipped' } } } }} />)
    expect(screen.getByTestId('rebuild-error-create')).toHaveTextContent('host h1 is not configured')
    expect(screen.getByRole('button', { name: /^rebuild$/i })).toBeEnabled()
  })

  it('shows why a re-point was refused', () => {
    render(<RebuildActionSet tabId="t1" paneId="p1" record={record} onRebuild={vi.fn()}
      operation={{ status: 'done', report: { hostId: 'h1', created: { code: 'new1', name: 'dev' }, repointed: false, steps: {
        create: { status: 'ok' }, resume: { status: 'skipped', error: 'not requested' },
        repoint: { status: 'failed', error: 'session new1 now belongs to tmux generation 333:3000' } } } }} />)
    expect(screen.getByTestId('rebuild-error-repoint'))
      .toHaveTextContent('session new1 now belongs to tmux generation 333:3000')
  })

  it('does not mistake a skipped step for a failure', () => {
    render(<RebuildActionSet tabId="t1" paneId="p1" record={record} onRebuild={vi.fn()}
      operation={{ status: 'done', report: { hostId: 'h1', created: { code: 'new1', name: 'dev' }, repointed: false, steps: {
        create: { status: 'ok' }, resume: { status: 'skipped', error: 'no resume command recorded' },
        repoint: { status: 'skipped', error: 'the pane binding changed' } } } }} />)
    expect(screen.queryByTestId('rebuild-error-create')).toBeNull()
    expect(screen.queryByTestId('rebuild-error-resume')).toBeNull()
    expect(screen.queryByTestId('rebuild-error-repoint')).toBeNull()
  })

  // === §7 limits copy ===

  it('surfaces the five documented limits', () => {
    render(<RebuildActionSet tabId="t1" paneId="p1" record={record} onRebuild={vi.fn()} />)
    for (const key of ['agent_only', 'minimal_flags', 'cwd_scoped', 'multi_pane', 'local_storage']) {
      expect(screen.getByTestId(`rebuild-limit-${key}`)).toBeInTheDocument()
    }
  })
})
