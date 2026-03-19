// spa/src/lib/api.ts
export interface Session {
  id: number
  uid: string
  name: string
  tmux_target: string
  cwd: string
  mode: string
  group_id: number
  sort_order: number
  cc_session_id: string
  cc_model: string
  has_relay: boolean
}

export async function listSessions(base: string): Promise<Session[]> {
  const res = await fetch(`${base}/api/sessions`)
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json()
}

export async function createSession(
  base: string, name: string, cwd: string, mode: string,
): Promise<Session> {
  const res = await fetch(`${base}/api/sessions`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, cwd, mode }),
  })
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json()
}

export async function deleteSession(base: string, id: number): Promise<void> {
  const res = await fetch(`${base}/api/sessions/${id}`, { method: 'DELETE' })
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
}

export async function switchMode(base: string, id: number, mode: string): Promise<Session> {
  const res = await fetch(`${base}/api/sessions/${id}/mode`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ mode }),
  })
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json()
}

// --- Handoff API ---

export async function handoff(
  base: string,
  id: number,
  mode: string,
  preset?: string,
): Promise<{ handoff_id: string }> {
  const body: Record<string, string> = { mode }
  if (preset) body.preset = preset
  const res = await fetch(`${base}/api/sessions/${id}/handoff`, {
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

// --- History API ---

export async function fetchHistory(base: string, sessionId: number): Promise<import('./stream-ws').StreamMessage[]> {
  const res = await fetch(`${base}/api/sessions/${sessionId}/history`)
  if (!res.ok) return []
  return res.json()
}

// --- Config API ---

export interface ConfigData {
  bind: string
  port: number
  terminal?: { auto_resize: boolean | null; ignore_size: boolean | null }
  stream: { presets: Array<{ name: string; command: string }> }
  jsonl: { presets: Array<{ name: string; command: string }> }
  detect: { cc_commands: string[]; poll_interval: number }
}

export async function getConfig(base: string): Promise<ConfigData> {
  const res = await fetch(`${base}/api/config`)
  if (!res.ok) throw new Error(`get config failed: ${res.status}`)
  return res.json()
}

export async function updateConfig(
  base: string,
  updates: Partial<ConfigData>,
): Promise<ConfigData> {
  const res = await fetch(`${base}/api/config`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(updates),
  })
  if (!res.ok) throw new Error(`update config failed: ${res.status}`)
  return res.json()
}
