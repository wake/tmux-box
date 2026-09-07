// spa/src/lib/host-api.ts — Host-aware API layer (unified)
import { useHostStore } from '../stores/useHostStore'
import type { StreamMessage } from './stream-ws'

/* ─── Shared types ─── */

export interface Session {
  code: string
  name: string
  cwd: string
  mode: string
  cc_session_id: string
  cc_model: string
  has_relay: boolean
  current_command?: string
  pane_title?: string
  window_name?: string
  // tmux server generation ("<server pid>:<start_time>") the daemon stamped on
  // the payload that carried this session. Changes on every tmux server
  // restart; "" means unknown (probe failure/timeout, or an older daemon) and
  // never counts as a match. Optional so older daemons stay consumable.
  tmux_instance?: string
}

export interface ConfigData {
  bind: string
  port: number
  upload_dir?: string
  terminal?: { sizing_mode: string }
  stream: { presets: Array<{ name: string; command: string }> }
  detect: { cc_commands: string[]; poll_interval: number }
}

/* ─── Core helpers ─── */

export function hostFetch(hostId: string, path: string, init?: RequestInit): Promise<Response> {
  const { getDaemonBase, getAuthHeaders } = useHostStore.getState()
  const base = getDaemonBase(hostId)
  const headers = new Headers(init?.headers)
  const auth = getAuthHeaders(hostId)
  for (const [k, v] of Object.entries(auth)) {
    headers.set(k, v)
  }
  return fetch(`${base}${path}`, { ...init, headers })
}

export function hostWsUrl(hostId: string, path: string): string {
  const base = useHostStore.getState().getWsBase(hostId)
  return `${base}${path}`
}

export async function fetchWsTicket(hostId: string): Promise<string> {
  const res = await hostFetch(hostId, '/api/ws-ticket', { method: 'POST' })
  if (!res.ok) throw new Error(`ws-ticket failed: ${res.status}`)
  const data = await res.json()
  return data.ticket
}

/* ─── API functions ─── */

export function fetchHealth(hostId: string) {
  return hostFetch(hostId, '/api/health')
}

export function fetchInfo(hostId: string) {
  return hostFetch(hostId, '/api/info')
}

export function fetchUploadStats(hostId: string) {
  return hostFetch(hostId, '/api/upload/stats')
}

export function fetchUploadFiles(hostId: string) {
  return hostFetch(hostId, '/api/upload/files')
}

export function deleteUploadFile(hostId: string, session: string, filename: string) {
  return hostFetch(hostId, `/api/upload/files/${session}/${filename}`, { method: 'DELETE' })
}

export function deleteUploadSession(hostId: string, session: string) {
  return hostFetch(hostId, `/api/upload/files/${session}`, { method: 'DELETE' })
}

export function deleteAllUploads(hostId: string) {
  return hostFetch(hostId, '/api/upload/files', { method: 'DELETE' })
}

export function renameSession(hostId: string, code: string, name: string) {
  return hostFetch(hostId, `/api/sessions/${code}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name }),
  })
}

/* ─── Session API ─── */

/**
 * An HTTP error that keeps its status code. The rebuild engine retries a
 * duplicate session name ONLY on 409 — the status the daemon returns
 * specifically for that case (`internal/module/session/handler.go:101-104`) —
 * and must never retry a 400 (validation) or 500 (create failure).
 */
export class HostApiError extends Error {
  status: number
  constructor(status: number, statusText: string) {
    super(`${status} ${statusText}`)
    this.name = 'HostApiError'
    this.status = status
  }
}

export async function listSessions(hostId: string): Promise<Session[]> {
  const res = await hostFetch(hostId, '/api/sessions')
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json()
}

export async function createSession(
  hostId: string, name: string, cwd: string, mode: string,
): Promise<Session> {
  const res = await hostFetch(hostId, '/api/sessions', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, cwd, mode }),
  })
  // Typed so a 409 (duplicate name) stays distinguishable from 400/500; the
  // message is unchanged, so existing callers that only render it are unaffected.
  if (!res.ok) throw new HostApiError(res.status, res.statusText)
  return res.json()
}

export async function deleteSession(hostId: string, code: string): Promise<void> {
  const res = await hostFetch(hostId, `/api/sessions/${code}`, { method: 'DELETE' })
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
}

export async function switchMode(hostId: string, code: string, mode: string): Promise<Session> {
  const res = await hostFetch(hostId, `/api/sessions/${code}/mode`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ mode }),
  })
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json()
}

/* ─── Handoff API ─── */

export async function handoff(
  hostId: string,
  code: string,
  mode: string,
  preset?: string,
): Promise<{ handoff_id: string }> {
  const body: Record<string, string> = { mode }
  if (preset) body.preset = preset
  const res = await hostFetch(hostId, `/api/sessions/${code}/handoff`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!res.ok) {
    const text = await res.text().catch(() => '')
    throw new Error(`handoff failed: ${res.status} ${text}`.trim())
  }
  return res.json()
}

/* ─── History API ─── */

export async function fetchHistory(hostId: string, sessionCode: string): Promise<StreamMessage[]> {
  const res = await hostFetch(hostId, `/api/sessions/${sessionCode}/history`)
  if (!res.ok) return []
  return res.json()
}

/**
 * A cwd reading and the tmux generation it was sampled in (spec §4.6.2).
 *
 * The pair travels together because a bare string cannot be attributed: a
 * caller that stamps the answer with the generation it *asked* with writes the
 * new session's directory into the old one's record whenever tmux restarted
 * and reused the code mid-request. `tmuxInstance` is `''` when unknown — an
 * old daemon, or a generation that moved during the read — and `''` never
 * equals a real generation, so unknown can only ever mean "do not write".
 */
export interface SessionCwd {
  cwd: string
  tmuxInstance: string
}

export async function fetchSessionCwd(
  hostId: string,
  sessionCode: string,
  signal?: AbortSignal,
): Promise<SessionCwd> {
  const res = await hostFetch(hostId, `/api/sessions/${sessionCode}/cwd`, { signal })
  if (!res.ok) throw new Error(`fetchSessionCwd failed: ${res.status}`)
  const body = await res.json()
  return { cwd: String(body.cwd ?? ''), tmuxInstance: String(body.tmux_instance ?? '') }
}

export async function fetchSessionHome(
  hostId: string,
  sessionCode: string,
  signal?: AbortSignal,
): Promise<string> {
  const res = await hostFetch(hostId, `/api/sessions/${sessionCode}/home`, { signal })
  if (!res.ok) throw new Error(`fetchSessionHome failed: ${res.status}`)
  const body = await res.json()
  return String(body.home ?? '')
}

/* ─── Config API ─── */

export async function getConfig(hostId: string): Promise<ConfigData> {
  const res = await hostFetch(hostId, '/api/config')
  if (!res.ok) throw new Error(`get config failed: ${res.status}`)
  return res.json()
}

export async function updateConfig(
  hostId: string,
  updates: Partial<ConfigData>,
): Promise<ConfigData> {
  const res = await hostFetch(hostId, '/api/config', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(updates),
  })
  if (!res.ok) throw new Error(`update config failed: ${res.status}`)
  return res.json()
}

/* ─── Agent Upload API ─── */

export async function agentUpload(
  hostId: string,
  file: File,
  session: string,
): Promise<{ filename: string; injected: boolean }> {
  const form = new FormData()
  form.append('file', file)
  form.append('session', session)
  const res = await hostFetch(hostId, '/api/agent/upload', {
    method: 'POST',
    body: form,
  })
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json()
}

/* ─── Monitor API ─── */

export interface MonitorMetricBounds {
  min: number
  max: number
}

export interface MonitorConfigBounds {
  refresh_interval_ms: MonitorMetricBounds
  top_process_limit: MonitorMetricBounds
}

export interface MonitorConfig {
  refresh_interval_ms: number
  top_process_limit: number
  bounds: MonitorConfigBounds
}

export type MonitorConfigUpdate = Partial<Pick<MonitorConfig, 'refresh_interval_ms' | 'top_process_limit'>>

export interface MonitorHostCPU {
  percent: number | null
  unavailable_reason: string | null
}

export interface MonitorHostMemory {
  total_bytes: number | null
  used_bytes: number | null
  used_percent: number | null
  unavailable_reason: string | null
}

export interface MonitorHostDisk {
  total_bytes: number | null
  used_bytes: number | null
  used_percent: number | null
  unavailable_reason: string | null
}

export interface MonitorHostMetrics {
  cpu: MonitorHostCPU
  memory: MonitorHostMemory
  disk: MonitorHostDisk
}

export interface MonitorTopProcess {
  pid: number
  ppid: number
  command: string
  cpu_percent: number
  memory_bytes: number
}

export interface MonitorTmuxSession {
  id: string
  name: string
}

export interface MonitorSessionDaemonMetrics {
  cpu_percent: number | null
  memory_bytes: number | null
  process_count: number | null
  top_processes: MonitorTopProcess[]
  unavailable_reason: string | null
}

export interface MonitorSessionMetrics {
  session_code: string
  tmux_session: MonitorTmuxSession
  daemon: MonitorSessionDaemonMetrics
}

export interface MonitorSnapshot {
  sampled_at: number
  host: MonitorHostMetrics
  sessions: MonitorSessionMetrics[]
  config: MonitorConfig
}

export async function fetchMonitorSnapshot(hostId: string): Promise<MonitorSnapshot> {
  const res = await hostFetch(hostId, '/api/monitor/snapshot')
  if (!res.ok) throw new Error(`fetchMonitorSnapshot failed: ${res.status}`)
  return res.json()
}

export async function fetchMonitorConfig(hostId: string): Promise<MonitorConfig> {
  const res = await hostFetch(hostId, '/api/monitor/config')
  if (!res.ok) throw new Error(`fetchMonitorConfig failed: ${res.status}`)
  return res.json()
}

export async function updateMonitorConfig(
  hostId: string,
  updates: MonitorConfigUpdate,
): Promise<MonitorConfig> {
  const res = await hostFetch(hostId, '/api/monitor/config', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(updates),
  })
  if (!res.ok) throw new Error(`updateMonitorConfig failed: ${res.status}`)
  return res.json()
}

/* ─── Agent Monitor API ─── */

export interface AgentMonitorChainSummary {
  chain_id: string
  started_at: number
  completed_at: number
  terminal_status: string
  terminal_reason: string
  tmux_session: string
  pane_id: string
  root_agent_type: string
  root_event_name: string
  root_reason: string
  latest_step_kind: string
  latest_decision: string
  latest_step_reason: string
  step_count: number
}

export interface AgentMonitorStep {
  step_id: string
  chain_id: string
  parent_step_id?: string
  seq: number
  kind: string
  tmux_session: string
  pane_id: string
  agent_type: string
  frame_id: string
  parent_frame_id: string
  event_name: string
  decision: string
  reason: string
  payload_json: string
  before_json: string
  after_json: string
  created_at: number
}

export interface AgentMonitorStepNode {
  step: AgentMonitorStep
  children: AgentMonitorStepNode[]
}

export interface AgentMonitorProjectionSummary {
  tmux_session: string
  pane_id: string
  primary_frame_id: string
  top_frame_id: string
  top_agent_type: string
  latest_chain_id: string
}

export async function fetchAgentMonitorChains(
  hostId: string,
  query = new URLSearchParams(),
): Promise<{ chains: AgentMonitorChainSummary[]; next_cursor: string }> {
  const qs = query.toString()
  const res = await hostFetch(hostId, `/api/agent/monitor/chains${qs ? `?${qs}` : ''}`)
  if (!res.ok) throw new Error(`fetchAgentMonitorChains failed: ${res.status}`)
  return res.json()
}

export async function fetchAgentMonitorChain(
  hostId: string,
  chainId: string,
): Promise<{ chain: AgentMonitorChainSummary; step_tree: AgentMonitorStepNode[] }> {
  const res = await hostFetch(hostId, `/api/agent/monitor/chains/${chainId}`)
  if (!res.ok) throw new Error(`fetchAgentMonitorChain failed: ${res.status}`)
  return res.json()
}

export async function fetchAgentMonitorProjection(
  hostId: string,
  query: URLSearchParams,
): Promise<{ projection: AgentMonitorProjectionSummary | null }> {
  const qs = query.toString()
  const res = await hostFetch(hostId, `/api/agent/monitor/projection${qs ? `?${qs}` : ''}`)
  if (!res.ok) throw new Error(`fetchAgentMonitorProjection failed: ${res.status}`)
  return res.json()
}

/* ─── Pairing API (Phase 5a) ─── */

/** POST /api/pair/verify — Quick mode: verify pairing secret, get setupSecret. */
export async function fetchPairVerify(
  base: string,
  secret: string,
): Promise<{ setupSecret: string }> {
  const res = await fetch(`${base}/api/pair/verify`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ secret }),
  })
  if (!res.ok) {
    const text = await res.text().catch(() => '')
    throw new PairingError(res.status, text)
  }
  return res.json()
}

/** POST /api/pair/setup — Quick mode: set token on daemon. */
export async function fetchPairSetup(
  base: string,
  setupSecret: string,
  token: string,
): Promise<{ ok: boolean }> {
  const res = await fetch(`${base}/api/pair/setup`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ setupSecret, token }),
  })
  if (!res.ok) {
    const text = await res.text().catch(() => '')
    throw new PairingError(res.status, text)
  }
  return res.json()
}

/** POST /api/token/auth — General mode: confirm runtime token. */
export async function fetchTokenAuth(
  base: string,
  token: string,
): Promise<{ ok: boolean }> {
  const res = await fetch(`${base}/api/token/auth`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${token}` },
  })
  if (res.status === 409) {
    // already_confirmed — treat as success per spec
    return { ok: true }
  }
  if (!res.ok) {
    const text = await res.text().catch(() => '')
    throw new PairingError(res.status, text)
  }
  return res.json()
}

export class PairingError extends Error {
  status: number
  body: string
  constructor(status: number, body: string) {
    super(`Pairing failed: HTTP ${status}`)
    this.status = status
    this.body = body
  }
}
