// spa/src/lib/rebuild/engine.ts — the rebuild operation (spec §4.8).
//
// One orchestrator over four steps: create → resume → re-point, with the cwd
// folded into the create. Three properties are load-bearing:
//
//  1. Every request goes through a transport pinned to `hostId` at operation
//     start (`transport.ts`), never through `hostFetch`.
//  2. The resume runs BEFORE the re-point. Clearing `terminated` swaps
//     `TerminatedPane` out (`SessionPaneContent.tsx:70-72`), unmounting the
//     panel that has to report the resume result.
//  3. A duplicate name is retried ONLY on HTTP 409, capped at 5.
//
// There is no rollback: a created session is a real session, so every partial
// failure keeps it named in the report.
import { pinHost, type PinnedTransport } from './transport'
import {
  useRebuildStore,
  withOperationLock,
  type OperationLockToken,
  type RebuildBinding,
} from '../../stores/useRebuildStore'
import { useTabStore } from '../../stores/useTabStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { findPane } from '../pane-tree'
import type { Session } from '../host-api'
import type { TmuxSessionContent } from '../../types/tab'

/** Which of the three editable rows the user left checked. */
export interface RebuildPlan {
  createSession: boolean
  applyCwd: boolean
  runResume: boolean
}

export interface StepResult {
  status: 'ok' | 'skipped' | 'failed'
  error?: string
}

export interface RebuildReport {
  hostId: string
  /** Survives every later failure — the session exists whatever happens next. */
  created?: { code: string; name: string; tmuxInstance: string }
  steps: { create: StepResult; resume: StepResult; repoint: StepResult }
  repointed: boolean
}

export interface RebuildDeps {
  createSession?: (hostId: string, name: string, cwd: string, mode: string) => Promise<Session>
  /**
   * `expectedTmuxInstance` is the generation the resume is authorised against
   * — the daemon refuses the keystroke when it does not hold (spec §4.6.2).
   */
  sendKeys?: (hostId: string, code: string, command: string, expectedTmuxInstance: string) => Promise<void>
  repoint?: (tabId: string, paneId: string, session: Session) => void
  /**
   * A lock the CALLER already holds (spec §4.11). "Rebuild all" takes the
   * operation lock once as `rebuild:batch` and hands its token to every group
   * it runs, because the lock is re-entrant per *owner* and `rebuild:<paneId>`
   * is a different owner — each group acquiring its own would simply be
   * refused. The token is verified against the current holder before it is
   * honoured, so a stale one cannot smuggle an operation past the lock.
   */
  lockToken?: OperationLockToken
}

/** `name`, `name-2`, … `name-5`. Five attempts, then the operation gives up. */
const CREATE_ATTEMPT_CAP = 5

function skipped(error?: string): StepResult {
  return error ? { status: 'skipped', error } : { status: 'skipped' }
}

function failed(err: unknown): StepResult {
  return { status: 'failed', error: err instanceof Error ? err.message : String(err) }
}

/**
 * The operation-lock owner for everything that acts on one pane (spec §4.11).
 * Pane-specific on purpose: two panes may not rebuild at once either, but the
 * owner is what tells the UI whose button to keep enabled.
 */
function paneOwner(paneId: string): string {
  return `rebuild:${paneId}`
}

/**
 * The same-pane refusal. The lock is re-entrant per owner, so without this a
 * second operation on ONE pane would be handed a re-entry token and run
 * concurrently with the first — the exact merge the lock exists to prevent.
 */
function paneBusy(paneId: string): boolean {
  return useRebuildStore.getState().operations[paneId]?.status === 'running'
}

/** A report for an operation that never started, so nothing is left half-run. */
function refusedReport(hostId: string, step: 'create' | 'resume', reason: string): RebuildReport {
  const steps: RebuildReport['steps'] = { create: skipped(), resume: skipped(), repoint: skipped() }
  steps[step] = { status: 'failed', error: reason }
  return { hostId, steps, repointed: false }
}

/**
 * Write a refusal into the store so the panel can show it.
 *
 * A refused or aborted operation never reaches `beginOperation`, and the panel
 * reads the store — so without this, pressing Rebuild against an unknown host,
 * a pane that moved, or a held operation lock just restored the button with no
 * reason given. The report is stored under the pane's CURRENT binding, which
 * is what makes it visible (`usePaneOperation`).
 *
 * Two entries are never traded for an error message: one that is still running,
 * and a finished one that created a session — its retries hang off it.
 */
function publishRefusal(
  hostId: string,
  tabId: string,
  paneId: string,
  plan: RebuildPlan,
  report: RebuildReport,
): RebuildReport {
  const existing = useRebuildStore.getState().operations[paneId]
  if (existing && (existing.status === 'running' || existing.createdSession)) return report
  const content = readTerminalPane(tabId, paneId)
  useRebuildStore.getState().beginOperation({
    paneId,
    tabId,
    hostId,
    plan,
    binding: content
      ? { hostId: content.hostId, sessionCode: content.sessionCode, tmuxInstance: content.tmuxInstance }
      : { hostId, sessionCode: '', tmuxInstance: '' },
    resumeCommand: content?.rebuild?.resumeCommand ?? '',
    report,
  })
  useRebuildStore.getState().finishOperation(paneId, { report })
  return report
}

/** The daemon returns 409 only for a duplicate session name; 400/500 never retry. */
function isDuplicateName(err: unknown): boolean {
  return typeof err === 'object' && err !== null
    && (err as { status?: unknown }).status === 409
}

/** The pane's live content, or null if it is gone / not a terminal tmux pane. */
function readTerminalPane(tabId: string, paneId: string): TmuxSessionContent | null {
  const tab = useTabStore.getState().tabs[tabId]
  if (!tab) return null
  const pane = findPane(tab.layout, paneId)
  const content = pane?.content
  if (!content || content.kind !== 'tmux-session' || content.mode !== 'terminal') return null
  return content
}

function bindingUnchanged(tabId: string, paneId: string, binding: RebuildBinding): boolean {
  const now = readTerminalPane(tabId, paneId)
  return !!now
    && now.hostId === binding.hostId
    && now.sessionCode === binding.sessionCode
    && now.tmuxInstance === binding.tmuxInstance
}

/**
 * Push the new session into `useSessionStore` the way `restore.ts`'s
 * `syncSessionStore` does: seed from the host's existing sessions so unrelated
 * live ones survive, drop the dead code so it cannot linger as a ghost, then
 * call `replaceHost` exactly once.
 *
 * The eviction is generation-scoped, like every other writer in this feature
 * (spec §4.5): after a tmux restart the dead pane's code can already belong to
 * a different, live session, and dropping THAT would make a real session
 * vanish from the session list and the picker until the next broadcast. The
 * cached entry is removed only when its generation stamp is the one the dead
 * binding carried — which includes the legacy both-unknown (`''`) case, so
 * panes with no recorded generation behave exactly as before.
 */
function syncSessionStore(hostId: string, session: Session, dead: { code: string; tmuxInstance: string }): void {
  const state = useSessionStore.getState()
  const byCode = new Map<string, Session>((state.sessions[hostId] ?? []).map((s) => [s.code, s]))
  const cached = byCode.get(dead.code)
  if (cached && (cached.tmux_instance ?? '') === dead.tmuxInstance) byCode.delete(dead.code)
  byCode.set(session.code, session)
  state.replaceHost(hostId, Array.from(byCode.values()))
}

/**
 * Step 4. Re-point the pane onto the created session and clear `terminated`.
 * The caller has already verified the binding, so this only writes.
 *
 * `rebuild.tmuxInstance` is re-stamped alongside `rebuild.sessionName`: the
 * field documents the generation the record describes, and after a rebuild
 * that is the new one — leaving the dead generation there would make the
 * record describe a session that no longer exists.
 */
function defaultRepoint(tabId: string, paneId: string, session: Session): void {
  const content = readTerminalPane(tabId, paneId)
  if (!content) return
  const tmuxInstance = session.tmux_instance ?? ''
  const next: TmuxSessionContent = {
    kind: 'tmux-session',
    hostId: content.hostId,
    sessionCode: session.code,
    mode: content.mode,
    cachedName: session.name,
    tmuxInstance,
    // `terminated` is deliberately absent — dropping it is what re-attaches the pane.
    rebuild: content.rebuild
      ? {
          ...content.rebuild,
          sessionName: session.name,
          tmuxInstance: tmuxInstance || content.rebuild.tmuxInstance,
        }
      : undefined,
  }
  useTabStore.getState().setPaneContent(tabId, paneId, next)
  syncSessionStore(content.hostId, session, { code: content.sessionCode, tmuxInstance: content.tmuxInstance })
}

/**
 * Re-point one further pane of a batch group onto the session the group's
 * source pane already created (spec §4.11): one create and one resume per
 * group, with EVERY pane in it re-pointed.
 *
 * Each member re-verifies its own binding first, against the baseline the
 * batch planned from — a pane that moved while the create was in flight keeps
 * whatever it moved to. Returns whether the pane was re-pointed.
 */
export function repointMember(
  tabId: string,
  paneId: string,
  binding: RebuildBinding,
  created: Session,
  repoint: NonNullable<RebuildDeps['repoint']> = defaultRepoint,
): boolean {
  if (!bindingUnchanged(tabId, paneId, binding)) return false
  repoint(tabId, paneId, created)
  return true
}

/** Everything the resume and re-point steps need, however they were reached. */
interface StepContext {
  hostId: string
  tabId: string
  paneId: string
  pinned: PinnedTransport
  binding: RebuildBinding
  resumeCommand: string
  /** The session step 1 created — absent when the plan skipped the create. */
  created?: Session
  /** Where the resume goes when nothing was created: the pane's own session. */
  fallbackCode: string
  report: RebuildReport
  sendKeys: NonNullable<RebuildDeps['sendKeys']>
  repoint: NonNullable<RebuildDeps['repoint']>
}

async function runResumeStep(ctx: StepContext, enabled: boolean): Promise<void> {
  if (!enabled) {
    ctx.report.steps.resume = skipped('not requested')
    return
  }
  if (!ctx.resumeCommand) {
    // Spec §4.7: a pane that never saw a qualifying SessionStart has no resume
    // command, and must not be given a guessed one.
    ctx.report.steps.resume = skipped('no resume command recorded')
    return
  }
  // The generation the keystroke is authorised against (spec §4.6.2). For a
  // session this operation created, that is the stamp the create response
  // carried; for the pane's own session, the generation its binding records.
  // Either can be '' — a daemon that could not read its own generation, or a
  // pane that never learnt one — and '' states no expectation, exactly as
  // every non-rebuild send-keys caller does. `useSessionStore` is deliberately
  // NOT consulted: a cache cannot prove what a code points at.
  const code = ctx.created?.code ?? ctx.fallbackCode
  const expectedTmuxInstance = ctx.created
    ? (ctx.created.tmux_instance ?? '')
    : ctx.binding.tmuxInstance
  try {
    ctx.pinned.assertUnchanged()
    await ctx.sendKeys(ctx.hostId, code, ctx.resumeCommand, expectedTmuxInstance)
    ctx.report.steps.resume = { status: 'ok' }
  } catch (err) {
    ctx.report.steps.resume = failed(err)
  }
}

function runRepointStep(ctx: StepContext): void {
  if (ctx.report.steps.resume.status === 'failed') {
    // The panel must stay mounted to report the failure and offer the retry.
    ctx.report.steps.repoint = skipped('resume failed')
    return
  }
  if (!ctx.created) {
    ctx.report.steps.repoint = skipped('nothing was created')
    return
  }
  if (!bindingUnchanged(ctx.tabId, ctx.paneId, ctx.binding)) {
    // Closed or re-pointed mid-flight. The created session stays in the report.
    ctx.report.steps.repoint = skipped('the pane binding changed')
    return
  }
  try {
    ctx.repoint(ctx.tabId, ctx.paneId, ctx.created)
    ctx.report.steps.repoint = { status: 'ok' }
    ctx.report.repointed = true
  } catch (err) {
    ctx.report.steps.repoint = failed(err)
  }
}

function publish(paneId: string, report: RebuildReport, created?: Session): void {
  useRebuildStore.getState().finishOperation(paneId, {
    report: { ...report, steps: { ...report.steps } },
    createdSession: created,
  })
}

/**
 * Recreate a pane's tmux session and put the pane back on it.
 *
 * Never throws: every failure lands in the returned report, which is also
 * written into `useRebuildStore` under `paneId`.
 *
 * Serialized against every other rebuild and against the legacy snapshot
 * actions by the shared operation lock (spec §4.11).
 */
export async function rebuildPane(
  hostId: string,
  tabId: string,
  paneId: string,
  plan: RebuildPlan,
  deps: RebuildDeps = {},
): Promise<RebuildReport> {
  const refuse = (reason: string) =>
    publishRefusal(hostId, tabId, paneId, plan, refusedReport(hostId, 'create', reason))

  if (paneBusy(paneId)) {
    return refuse(`a rebuild is already running for pane ${paneId}`)
  }
  const borrowed = deps.lockToken
  if (borrowed) {
    const holder = useRebuildStore.getState().lockedBy
    if (holder !== borrowed.owner) {
      return refuse(`the caller's operation lock (${borrowed.owner}) is no longer held`)
    }
    // Run inside the caller's lock — and never release it: the batch is not
    // finished when one group is.
    return runRebuild(hostId, tabId, paneId, plan, deps)
  }
  return withOperationLock(
    paneOwner(paneId),
    () => runRebuild(hostId, tabId, paneId, plan, deps),
    (holder) => refuse(`another operation is already running (${holder})`),
  )
}

async function runRebuild(
  hostId: string,
  tabId: string,
  paneId: string,
  plan: RebuildPlan,
  deps: RebuildDeps,
): Promise<RebuildReport> {
  const report: RebuildReport = {
    hostId,
    steps: { create: skipped(), resume: skipped(), repoint: skipped() },
    repointed: false,
  }

  // Pin FIRST: an unknown host must stop the operation here, not silently
  // resolve to the active host inside the first request.
  let pinned: PinnedTransport
  try {
    pinned = pinHost(hostId)
  } catch (err) {
    report.steps.create = failed(err)
    return publishRefusal(hostId, tabId, paneId, plan, report)
  }

  const content = readTerminalPane(tabId, paneId)
  if (!content || content.hostId !== hostId) {
    report.steps.create = failed(new Error(`pane ${paneId} is no longer a terminal pane on ${hostId}`))
    return publishRefusal(hostId, tabId, paneId, plan, report)
  }

  const binding: RebuildBinding = {
    hostId,
    sessionCode: content.sessionCode,
    tmuxInstance: content.tmuxInstance,
  }
  const record = content.rebuild
  const baseName = record?.sessionName || content.cachedName
  const resumeCommand = record?.resumeCommand ?? ''
  const cwd = plan.applyCwd ? (record?.cwd ?? '') : ''

  useRebuildStore.getState().beginOperation({
    paneId, tabId, hostId, plan, binding, host: pinned.identity, resumeCommand, report,
  })

  const createSession = deps.createSession
    ?? ((_hostId: string, name: string, dir: string, mode: string) => pinned.createSession(name, dir, mode))
  const sendKeys = deps.sendKeys
    ?? ((_hostId: string, code: string, command: string, expected: string) => pinned.sendKeys(code, command, expected))
  const repoint = deps.repoint ?? defaultRepoint

  let created: Session | undefined
  if (plan.createSession) {
    for (let attempt = 1; attempt <= CREATE_ATTEMPT_CAP; attempt++) {
      const name = attempt === 1 ? baseName : `${baseName}-${attempt}`
      try {
        pinned.assertUnchanged()
        created = await createSession(hostId, name, cwd, 'terminal')
        report.steps.create = { status: 'ok' }
        // The daemon's response is authoritative for the name actually used.
        report.created = {
          code: created.code,
          name: created.name,
          tmuxInstance: created.tmux_instance ?? '',
        }
        break
      } catch (err) {
        if (isDuplicateName(err) && attempt < CREATE_ATTEMPT_CAP) continue
        report.steps.create = failed(err)
        break
      }
    }
    if (!created) {
      publish(paneId, report)
      return report
    }
  } else {
    report.steps.create = skipped('not requested')
  }

  const ctx: StepContext = {
    hostId, tabId, paneId, pinned, binding, resumeCommand, created,
    fallbackCode: content.sessionCode, report, sendKeys, repoint,
  }
  await runResumeStep(ctx, plan.runResume)
  runRepointStep(ctx)

  publish(paneId, report, created)
  return report
}

/**
 * Whether the code the operation created still belongs to the session it
 * created, expressed as the reason it does not.
 *
 * A retry acts on a session created some time ago, and tmux hands out session
 * codes again after a restart (spec §3.5, §4.5) — so the code alone is not an
 * identity. The generation stamp that arrived with the create response is
 * compared against the one on the session the SPA currently sees at that code.
 *
 * Unknown is not mismatched: no session at that code (the daemon will reject
 * the send-keys, harmlessly), or an empty stamp on either side, means there is
 * no evidence of a stranger — the same rule §4.6 applies to death detection.
 */
function targetIdentityMismatch(hostId: string, created: Session): string | null {
  const createdInstance = created.tmux_instance ?? ''
  if (!createdInstance) return null
  const live = (useSessionStore.getState().sessions[hostId] ?? []).find((s) => s.code === created.code)
  const liveInstance = live?.tmux_instance ?? ''
  if (!liveInstance || liveInstance === createdInstance) return null
  return `session ${created.code} on ${hostId} now belongs to tmux generation ${liveInstance}, not the ${createdInstance} this rebuild created`
}

/**
 * Stop the tail before it sends or re-points: the target is not provably the
 * one the operation created, so neither action is safe.
 */
function refuseTail(report: RebuildReport, withResume: boolean, reason: string): void {
  const step: StepResult = { status: 'failed', error: reason }
  report.steps.resume = withResume ? step : skipped('not requested')
  report.steps.repoint = step
  report.repointed = false
}

/**
 * A refusal on the retry path. The operation itself is left alone — its created
 * session is the whole reason a retry exists — but its report picks up the
 * reason, so the panel says why instead of just re-enabling the button.
 */
function publishTailRefusal(paneId: string, withResume: boolean, reason: string): RebuildReport {
  const op = useRebuildStore.getState().operations[paneId]
  if (!op) return refusedReport('', 'resume', reason)
  const report: RebuildReport = { ...op.report, steps: { ...op.report.steps }, repointed: false }
  refuseTail(report, withResume, reason)
  publish(paneId, report, op.createdSession)
  return report
}

/** The shared tail of "Retry resume" and "Attach anyway". */
async function resumeTail(paneId: string, withResume: boolean, deps: RebuildDeps): Promise<RebuildReport> {
  const op = useRebuildStore.getState().operations[paneId]
  // No created session and no pinned host both mean the same thing: nothing
  // that a retry could act on. Only a refusal is stored without a host.
  if (!op || !op.createdSession || !op.host) {
    return refusedReport(op?.hostId ?? '', 'resume', `no completed rebuild is recorded for pane ${paneId}`)
  }
  if (op.status === 'running') {
    return refusedReport(op.hostId, 'resume', `a rebuild is already running for pane ${paneId}`)
  }
  return withOperationLock(
    paneOwner(paneId),
    () => runResumeTail(paneId, withResume, deps),
    (holder) => publishTailRefusal(paneId, withResume, `another operation is already running (${holder})`),
  )
}

async function runResumeTail(paneId: string, withResume: boolean, deps: RebuildDeps): Promise<RebuildReport> {
  const op = useRebuildStore.getState().operations[paneId]
  // Re-read under the lock; `resumeTail` already proved all of these hold.
  if (!op || !op.createdSession || !op.host) {
    return refusedReport(op?.hostId ?? '', 'resume', `no completed rebuild is recorded for pane ${paneId}`)
  }
  // Back to `running` so the same-pane refusal above covers a retry too.
  useRebuildStore.getState().patchOperation(paneId, { status: 'running' })

  // Carry the create result forward: the session it made is still the subject.
  const report: RebuildReport = { ...op.report, steps: { ...op.report.steps }, repointed: false }

  let pinned: PinnedTransport
  try {
    // Against the identity the operation pinned, NOT whatever the host id
    // resolves to now: the resume belongs to the machine that created it.
    pinned = pinHost(op.hostId, op.host)
  } catch (err) {
    refuseTail(report, withResume, err instanceof Error ? err.message : String(err))
    publish(paneId, report, op.createdSession)
    return report
  }

  const mismatch = targetIdentityMismatch(op.hostId, op.createdSession)
  if (mismatch) {
    refuseTail(report, withResume, mismatch)
    publish(paneId, report, op.createdSession)
    return report
  }

  const ctx: StepContext = {
    hostId: op.hostId,
    tabId: op.tabId,
    paneId,
    pinned,
    binding: op.binding,
    resumeCommand: op.resumeCommand,
    created: op.createdSession,
    fallbackCode: op.binding.sessionCode,
    report,
    sendKeys: deps.sendKeys
      ?? ((_hostId: string, code: string, command: string, expected: string) => pinned.sendKeys(code, command, expected)),
    repoint: deps.repoint ?? defaultRepoint,
  }
  await runResumeStep(ctx, withResume)
  runRepointStep(ctx)

  publish(paneId, report, op.createdSession)
  return report
}

/**
 * Re-send the resume against the session the failed operation already created,
 * then finish the operation by re-pointing the pane onto it.
 */
export function retryResume(paneId: string, deps: RebuildDeps = {}): Promise<RebuildReport> {
  return resumeTail(paneId, true, deps)
}

/** Give up on the resume and just re-point the pane onto the created session. */
export function attachAnyway(paneId: string, deps: RebuildDeps = {}): Promise<RebuildReport> {
  return resumeTail(paneId, false, deps)
}
