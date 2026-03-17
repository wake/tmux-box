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
  preset: string,
): Promise<{ handoff_id: string }> {
  const res = await fetch(`${base}/api/sessions/${id}/handoff`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ mode, preset }),
  })
  if (!res.ok) throw new Error(`handoff failed: ${res.status}`)
  return res.json()
}

// --- Config API ---

export interface ConfigData {
  bind: string
  port: number
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
