// spa/src/lib/api.ts
export interface Session {
  id: number
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
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ mode }),
  })
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json()
}
