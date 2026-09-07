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
import { bindingEquals } from './binding'
import {
  useRebuildStore,
  withOperationLock,
  type OperationLockGrant,
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
  /**
   * A refusal, as opposed to a failure — the daemon proved the target is not
   * the session this operation created (spec §4.6.2). Nothing the user can
   * press may clear it: re-sending can only be refused again, and re-pointing
   * would bind the pane to whatever now owns that code.
   */
  refusal?: 'generation'
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
  /**
   * Read one session from the daemon, or `null` when it answers "not found".
   * The retry path uses it to obtain the generation that currently owns the
   * code it created (spec §4.6.2) — the SPA cache cannot prove that, and its
   * silence least of all. Defaults to the pinned transport.
   */
  getSession?: (hostId: string, code: string) => Promise<Session | null>
  repoint?: (tabId: string, paneId: string, session: Session) => void
  /**
   * A lock the CALLER already holds (spec §4.11). "Rebuild all" takes the
   * operation lock once as `rebuild:batch` and hands its grant to every group
   * it runs; a group acquiring its own would simply be refused, because the
   * lock only ever has one holder. The grant is what proves the caller is
   * genuinely inside that lock — a name would not, and a stale grant is
   * refused, so neither can smuggle an operation past it.
   */
  lockGrant?: OperationLockGrant
  /**
   * The binding the CALLER planned this operation from (spec §4.11).
   *
   * "Rebuild all" snapshots every group up front and then runs them in
   * sequence, so a later group's source pane can be re-pointed — through the
   * session picker, say — while an earlier group is still in flight. The engine
   * otherwise reads whatever the pane holds NOW as its baseline, and would
   * rebuild the session the user just chose, then drag the rest of the group
   * onto that wrong result. Verified against the pane's live binding in the
   * same synchronous step that reaches the create, so nothing can slip between
   * the check and the request.
   *
   * Absent for a single-pane rebuild: there the pane's current binding IS the
   * baseline, decided at the moment the user pressed the button.
   */
  expectedBinding?: RebuildBinding
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

/**
 * The send-keys 409: the generation precondition did not hold and the daemon
 * sent nothing (`handler.go`). Matched by status rather than by class so a
 * caller-supplied `sendKeys` reporting the same refusal is honoured too — the
 * create step's 409 (a duplicate name) is a different endpoint, never here.
 */
function isGenerationConflict(err: unknown): boolean {
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

/** Exact, not legacy-compatible: see `binding.ts` for why the two differ. */
function bindingUnchanged(tabId: string, paneId: string, binding: RebuildBinding): boolean {
  const now = readTerminalPane(tabId, paneId)
  return !!now && bindingEquals(now, binding)
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

/** Whether a member was re-pointed, and the reason when it was not. */
export interface MemberRepointResult {
  repointed: boolean
  reason?: string
}

/**
 * Re-point one further pane of a batch group onto the session the group's
 * source pane already created (spec §4.11): one create and one resume per
 * group, with EVERY pane in it re-pointed.
 *
 * Two guards, in the order that gives the truer reason. `assertHostUnchanged`
 * re-asserts the host the group pinned — a session code only means anything on
 * the machine that issued it, and the pane's `hostId` is re-resolved every
 * time the terminal builds its URL. Then each member re-verifies its own
 * binding against the baseline the batch planned from, so a pane that moved
 * while the create was in flight keeps whatever it moved to.
 */
export function repointMember(
  tabId: string,
  paneId: string,
  binding: RebuildBinding,
  created: Session,
  repoint: NonNullable<RebuildDeps['repoint']> = defaultRepoint,
  assertHostUnchanged?: () => void,
): MemberRepointResult {
  if (assertHostUnchanged) {
    try {
      assertHostUnchanged()
    } catch (err) {
      return { repointed: false, reason: err instanceof Error ? err.message : String(err) }
    }
  }
  if (!bindingUnchanged(tabId, paneId, binding)) {
    return { repointed: false, reason: 'the pane binding changed' }
  }
  repoint(tabId, paneId, created)
  return { repointed: true }
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
  // `useSessionStore` is deliberately NOT consulted: a cache cannot prove what
  // a code points at.
  const code = ctx.created?.code ?? ctx.fallbackCode
  const expectedTmuxInstance = ctx.created
    ? (ctx.created.tmux_instance ?? '')
    : ctx.binding.tmuxInstance
  if (!expectedTmuxInstance) {
    // Either the daemon could not read its own generation when it answered the
    // create, or the pane never learnt one. Both mean the same thing: there is
    // nothing to assert, so nothing authorises the keystroke. Omitting the
    // precondition — which is what Quick Commands legitimately does, having no
    // generation to state — would let a retry after a tmux restart type the
    // resume command into a stranger's session at the same code.
    ctx.report.steps.resume = {
      status: 'failed',
      error: ctx.created
        ? `the daemon did not report a tmux generation for the session it created (${code}), so the resume cannot be authorised`
        : `pane ${ctx.paneId} records no tmux generation, so the resume cannot be authorised`,
    }
    return
  }
  try {
    ctx.pinned.assertUnchanged()
    await ctx.sendKeys(ctx.hostId, code, ctx.resumeCommand, expectedTmuxInstance)
    ctx.report.steps.resume = { status: 'ok' }
  } catch (err) {
    // A 409 is the daemon's generation refusal: it held the code against the
    // generation the request named and they did not match. Typed, so no later
    // action treats it as a transient failure worth working around.
    ctx.report.steps.resume = isGenerationConflict(err)
      ? { ...failed(err), refusal: 'generation' }
      : failed(err)
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
  try {
    // The host may have been re-addressed while the create or the send-keys
    // was awaited. The code belongs to the machine that issued it, but the
    // pane's `hostId` resolves fresh on every attach — writing the code now
    // would point the terminal at a stranger's session of the same code. Read
    // before the binding check: it is the more fundamental of the two.
    ctx.pinned.assertUnchanged()
  } catch (err) {
    // Nothing is written to the pane or the session store; the created session
    // stays named in the report.
    ctx.report.steps.repoint = failed(err)
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
  const borrowed = deps.lockGrant
  if (borrowed) {
    // Re-enter the caller's lock, which only its own grant can open. The
    // re-entry grant's release is a no-op, so a finished group never drops the
    // lock the batch around it is still relying on.
    return withOperationLock(
      paneOwner(paneId),
      () => runRebuild(hostId, tabId, paneId, plan, deps),
      () => refuse(`the caller's operation lock (${borrowed.owner}) is no longer held`),
      borrowed,
    )
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
  // The caller's baseline, checked here rather than at plan time: everything
  // from this line to the create below is one synchronous run.
  if (deps.expectedBinding && !bindingEquals(binding, deps.expectedBinding)) {
    const want = deps.expectedBinding
    report.steps.create = failed(new Error(
      `pane ${paneId} no longer holds the binding this operation planned from `
      + `(${want.sessionCode}@${want.tmuxInstance || 'unknown'} → `
      + `${binding.sessionCode}@${binding.tmuxInstance || 'unknown'})`,
    ))
    return publishRefusal(hostId, tabId, paneId, plan, report)
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
 * created, expressed as the reason it does not — or `null` when the daemon
 * confirms it does.
 *
 * A retry acts on a session created some time ago, and tmux hands out session
 * codes again after a restart (spec §3.5, §4.5), so the code alone is not an
 * identity. The generation that arrived with the create response is compared
 * against one obtained from the PINNED DAEMON now. `useSessionStore` used to
 * answer this, and could not: a missing entry and a stale entry both read as
 * "no evidence of a stranger", which was then treated as permission. Absence
 * of contrary evidence is not authorisation — only a positive match is
 * (spec §4.6.2).
 *
 * Every unknown therefore refuses: no generation on the create response, a
 * daemon that cannot be reached, a code it no longer knows, or a generation it
 * cannot read. None of them are proof of a stranger, and none of them are
 * permission either.
 */
async function targetIdentityRefusal(
  getSession: NonNullable<RebuildDeps['getSession']>,
  hostId: string,
  created: Session,
): Promise<string | null> {
  const createdInstance = created.tmux_instance ?? ''
  if (!createdInstance) {
    return `this rebuild never learnt which tmux generation session ${created.code} belongs to, so it cannot be re-attached`
  }
  let live: Session | null
  try {
    live = await getSession(hostId, created.code)
  } catch (err) {
    const detail = err instanceof Error ? err.message : String(err)
    return `could not confirm which tmux generation session ${created.code} on ${hostId} belongs to: ${detail}`
  }
  if (!live) return `session ${created.code} no longer exists on ${hostId}`
  const liveInstance = live.tmux_instance ?? ''
  if (!liveInstance) {
    return `${hostId} could not report which tmux generation session ${created.code} belongs to`
  }
  if (liveInstance !== createdInstance) {
    return `session ${created.code} on ${hostId} now belongs to tmux generation ${liveInstance}, not the ${createdInstance} this rebuild created`
  }
  return null
}

/**
 * Stop the tail before it sends or re-points: the target is not provably the
 * one the operation created, so neither action is safe.
 *
 * A `refusal` outranks `withResume`. "Attach anyway" normally records the
 * resume as skipped, but a generation refusal has to stay on the report it
 * came from — recording it as "not requested" would erase the very state that
 * is meant to be unclearable.
 */
function refuseTail(
  report: RebuildReport,
  withResume: boolean,
  reason: string,
  refusal?: StepResult['refusal'],
): void {
  const step: StepResult = refusal
    ? { status: 'failed', error: reason, refusal }
    : { status: 'failed', error: reason }
  report.steps.resume = withResume || refusal ? step : skipped('not requested')
  report.steps.repoint = { status: 'failed', error: reason }
  report.repointed = false
}

/**
 * A refusal on the retry path. The operation itself is left alone — its created
 * session is the whole reason a retry exists — but its report picks up the
 * reason, so the panel says why instead of just re-enabling the button.
 */
function publishTailRefusal(
  paneId: string,
  withResume: boolean,
  reason: string,
  refusal?: StepResult['refusal'],
): RebuildReport {
  const op = useRebuildStore.getState().operations[paneId]
  if (!op) return refusedReport('', 'resume', reason)
  const report: RebuildReport = { ...op.report, steps: { ...op.report.steps }, repointed: false }
  refuseTail(report, withResume, reason, refusal)
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
  if (op.report.steps.resume.refusal === 'generation') {
    // Already refused by the daemon, on evidence only it holds. Neither button
    // may argue with that: the code named in the refusal is somebody else's.
    return publishTailRefusal(paneId, withResume, op.report.steps.resume.error
      ?? `session ${op.createdSession.code} belongs to another tmux generation`, 'generation')
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

  const getSession = deps.getSession
    ?? ((_hostId: string, code: string) => pinned.getSession(code))
  const refusal = await targetIdentityRefusal(getSession, op.hostId, op.createdSession)
  if (refusal) {
    refuseTail(report, withResume, refusal)
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
