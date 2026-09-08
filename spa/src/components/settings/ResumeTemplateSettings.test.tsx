// spa/src/components/settings/ResumeTemplateSettings.test.tsx
//
// Task 16 — the per-agent resume command template editor (spec §4.5).
//
// `fetch` is stubbed rather than `host-api`, because two of the five contracts
// this component is the only place to honour live in the request itself: the
// body must carry the COMMAND WORD (`cld-yolo`, not the whole template), and a
// 404 from an older daemon must surface as `unverifiable` rather than as an
// error the user has to debug.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, act, waitFor } from '@testing-library/react'
import { ResumeTemplateSettings } from './ResumeTemplateSettings'
import componentSource from './ResumeTemplateSettings.tsx?raw'
import { AGENT_NAMES } from '../../lib/agent-metadata'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { DEFAULT_RESUME_TEMPLATES, useResumeTemplateStore } from '../../stores/useResumeTemplateStore'
import en from '../../locales/en.json'
import zhTW from '../../locales/zh-TW.json'

const H1 = 'host-1'
const H2 = 'host-2'

function host(id: string, name: string, order: number) {
  return { id, name, ip: '100.64.0.2', port: 7860 + order, token: 'purdex_t', order }
}

/** A fetch stub whose promise is settled by the test. */
function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function input(agent: string, field: 'exact' | 'fallback') {
  return screen.getByTestId(`resume-template-input-${agent}-${field}`) as HTMLInputElement
}

function testButton(agent: string, field: 'exact' | 'fallback') {
  return screen.getByTestId(`resume-template-test-${agent}-${field}`)
}

function verdict(agent: string, field: 'exact' | 'fallback') {
  return screen.queryByTestId(`resume-template-verdict-${agent}-${field}`)
}

function lastFetchBody(): Record<string, unknown> {
  const calls = vi.mocked(globalThis.fetch).mock.calls
  const init = calls[calls.length - 1][1] as RequestInit
  return JSON.parse(String(init.body))
}

beforeEach(() => {
  vi.restoreAllMocks()
  useResumeTemplateStore.setState({ agents: {} })
  useHostStore.setState({
    hosts: { [H1]: host(H1, 'mlab', 0), [H2]: host(H2, 'air', 1) },
    hostOrder: [H1, H2],
    activeHostId: H1,
  })
})

describe('ResumeTemplateSettings — rows', () => {
  it('renders a row pair per AGENT_NAMES agent, pre-filled with the defaults', () => {
    render(<ResumeTemplateSettings />)
    for (const [agent, pair] of Object.entries(DEFAULT_RESUME_TEMPLATES)) {
      expect(input(agent, 'exact').value).toBe(pair.exact)
      expect(input(agent, 'fallback').value).toBe(pair.fallback)
    }
    expect(screen.getByTestId('resume-template-agent-cc').textContent).toContain('Claude Code')
  })

  it('Enter commits the edit to the store, and the trailing blur does not commit twice', () => {
    const spy = vi.spyOn(useResumeTemplateStore.getState(), 'setTemplate')
    render(<ResumeTemplateSettings />)
    const el = input('cc', 'exact')
    fireEvent.change(el, { target: { value: 'cld-yolo --resume {id}' } })
    fireEvent.keyDown(el, { key: 'Enter' })
    fireEvent.blur(el)

    expect(spy).toHaveBeenCalledTimes(1)
    expect(useResumeTemplateStore.getState().agents.cc?.exact).toBe('cld-yolo --resume {id}')
  })

  it('blur alone commits', () => {
    render(<ResumeTemplateSettings />)
    const el = input('codex', 'fallback')
    fireEvent.change(el, { target: { value: 'codex resume --last --yolo' } })
    fireEvent.blur(el)
    expect(useResumeTemplateStore.getState().agents.codex?.fallback).toBe('codex resume --last --yolo')
  })

  it('an IME Enter does not commit — the keystroke belongs to the candidate', () => {
    render(<ResumeTemplateSettings />)
    const el = input('cc', 'exact')
    fireEvent.compositionStart(el)
    fireEvent.change(el, { target: { value: '重新開始 {id}' } })
    fireEvent.keyDown(el, { key: 'Enter', isComposing: true })
    expect(useResumeTemplateStore.getState().agents.cc).toBeUndefined()

    fireEvent.compositionEnd(el)
    fireEvent.keyDown(el, { key: 'Enter' })
    expect(useResumeTemplateStore.getState().agents.cc?.exact).toBe('重新開始 {id}')
  })

  it('an Enter carrying isComposing does not commit, even without a compositionstart', () => {
    // The other half of the guard: some IMEs fire the keydown with
    // `isComposing` set and no composition event we saw first.
    render(<ResumeTemplateSettings />)
    const el = input('cc', 'exact')
    fireEvent.change(el, { target: { value: 'half {id}' } })
    fireEvent.keyDown(el, { key: 'Enter', isComposing: true })
    expect(useResumeTemplateStore.getState().agents.cc).toBeUndefined()
  })

  it('Escape reverts the row and commits nothing', () => {
    render(<ResumeTemplateSettings />)
    const el = input('cc', 'exact')
    fireEvent.change(el, { target: { value: 'nonsense' } })
    fireEvent.keyDown(el, { key: 'Escape' })
    expect(useResumeTemplateStore.getState().agents.cc).toBeUndefined()
    expect(input('cc', 'exact').value).toBe(DEFAULT_RESUME_TEMPLATES.cc.exact)
  })

  it('`busy` disables every input and every Test button', () => {
    // Every row, not just the first: `busy` is a panel-level prop, and a row
    // left editable under it would be overwritten by the action's result.
    render(<ResumeTemplateSettings busy />)
    for (const agent of Object.keys(AGENT_NAMES)) {
      for (const field of ['exact', 'fallback'] as const) {
        expect(input(agent, field).disabled, `input ${agent}/${field}`).toBe(true)
        expect(testButton(agent, field), `test button ${agent}/${field}`).toBeDisabled()
      }
    }
  })

  it('Reset all drops every customisation and repaints the defaults', () => {
    useResumeTemplateStore.setState({ agents: { cc: { exact: 'x {id}', fallback: 'y' } } })
    render(<ResumeTemplateSettings />)
    expect(input('cc', 'exact').value).toBe('x {id}')

    fireEvent.click(screen.getByTestId('resume-template-reset'))
    expect(useResumeTemplateStore.getState().agents).toEqual({})
    expect(input('cc', 'exact').value).toBe(DEFAULT_RESUME_TEMPLATES.cc.exact)
  })
})

describe('ResumeTemplateSettings — warnings never block the save', () => {
  it('an `exact` without {id} warns and still saves', () => {
    render(<ResumeTemplateSettings />)
    const el = input('cc', 'exact')
    fireEvent.change(el, { target: { value: 'claude -c' } })
    fireEvent.keyDown(el, { key: 'Enter' })

    expect(screen.getByTestId('resume-template-warning-cc-exact')).toBeTruthy()
    expect(useResumeTemplateStore.getState().agents.cc?.exact).toBe('claude -c')
  })

  it('a `fallback` with {id} warns and still saves', () => {
    render(<ResumeTemplateSettings />)
    const el = input('cc', 'fallback')
    fireEvent.change(el, { target: { value: 'claude --resume {id}' } })
    fireEvent.keyDown(el, { key: 'Enter' })

    expect(screen.getByTestId('resume-template-warning-cc-fallback')).toBeTruthy()
    expect(useResumeTemplateStore.getState().agents.cc?.fallback).toBe('claude --resume {id}')
  })

  it('the defaults raise no warning', () => {
    render(<ResumeTemplateSettings />)
    expect(screen.queryByTestId('resume-template-warning-cc-exact')).toBeNull()
    expect(screen.queryByTestId('resume-template-warning-cc-fallback')).toBeNull()
  })
})

describe('ResumeTemplateSettings — the probe', () => {
  it('POSTs only the command word, with {id} left unsubstituted', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValue(jsonResponse({ resolved: true, detail: '/Users/wake/.local/bin/cld-yolo' }))
    render(<ResumeTemplateSettings />)
    const el = input('cc', 'exact')
    fireEvent.change(el, { target: { value: 'cld-yolo --resume {id}' } })
    fireEvent.keyDown(el, { key: 'Enter' })

    await act(async () => { fireEvent.click(testButton('cc', 'exact')) })

    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toBe('http://100.64.0.2:7860/api/shell/resolve-command')
    expect(init.method).toBe('POST')
    expect(lastFetchBody()).toEqual({ command: 'cld-yolo' })
  })

  it('probes the uncommitted draft word too, so Test judges what is on screen', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ resolved: true, detail: 'x' }))
    render(<ResumeTemplateSettings />)
    fireEvent.change(input('cc', 'exact'), { target: { value: 'wrapper --resume {id}' } })
    await act(async () => { fireEvent.click(testButton('cc', 'exact')) })
    expect(lastFetchBody()).toEqual({ command: 'wrapper' })
  })

  it('renders a resolved verdict with the detail the daemon printed', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ resolved: true, detail: "alias cld='cld-yolo'" }))
    render(<ResumeTemplateSettings />)
    await act(async () => { fireEvent.click(testButton('cc', 'exact')) })

    const el = verdict('cc', 'exact')!
    expect(el.getAttribute('data-status')).toBe('resolved')
    expect(el.textContent).toContain("alias cld='cld-yolo'")
  })

  it.each(['not_found', 'shell_metacharacters', 'too_long', 'timeout', 'shell_failed'])(
    'renders the %s verdict',
    async (reason) => {
      vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ resolved: false, reason }))
      render(<ResumeTemplateSettings />)
      await act(async () => { fireEvent.click(testButton('cc', 'exact')) })

      const el = verdict('cc', 'exact')!
      expect(el.getAttribute('data-status')).toBe('unresolved')
      expect(el.getAttribute('data-reason')).toBe(reason)
      expect(el.textContent).toBe(en[`resume_template.verdict.${reason}` as keyof typeof en])
    },
  )

  it('a 404 from an older daemon renders as unverifiable, and the template stays saved', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('not found', { status: 404 }))
    render(<ResumeTemplateSettings />)
    const el = input('cc', 'exact')
    fireEvent.change(el, { target: { value: 'cld-yolo --resume {id}' } })
    fireEvent.keyDown(el, { key: 'Enter' })

    await act(async () => { fireEvent.click(testButton('cc', 'exact')) })

    expect(verdict('cc', 'exact')!.getAttribute('data-status')).toBe('unverifiable')
    expect(useResumeTemplateStore.getState().agents.cc?.exact).toBe('cld-yolo --resume {id}')
  })

  it('a network rejection renders as unverifiable, and the template stays saved', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new TypeError('Failed to fetch'))
    render(<ResumeTemplateSettings />)
    const el = input('cc', 'fallback')
    fireEvent.change(el, { target: { value: 'cld-yolo -c' } })
    fireEvent.keyDown(el, { key: 'Enter' })

    await act(async () => { fireEvent.click(testButton('cc', 'fallback')) })

    expect(verdict('cc', 'fallback')!.getAttribute('data-status')).toBe('unverifiable')
    expect(useResumeTemplateStore.getState().agents.cc?.fallback).toBe('cld-yolo -c')
  })
})

describe('ResumeTemplateSettings — the host picker', () => {
  it('defaults to the active host and sends the probe there', async () => {
    useHostStore.setState({ activeHostId: H2 })
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ resolved: true, detail: 'x' }))
    render(<ResumeTemplateSettings />)

    expect((screen.getByTestId('resume-template-host') as HTMLSelectElement).value).toBe(H2)
    await act(async () => { fireEvent.click(testButton('cc', 'exact')) })
    expect(fetchSpy.mock.calls[0][0]).toBe('http://100.64.0.2:7861/api/shell/resolve-command')
  })

  it('switching host clears a verdict already on screen', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ resolved: true, detail: 'x' }))
    render(<ResumeTemplateSettings />)
    await act(async () => { fireEvent.click(testButton('cc', 'exact')) })
    expect(verdict('cc', 'exact')).toBeTruthy()

    fireEvent.change(screen.getByTestId('resume-template-host'), { target: { value: H2 } })
    expect(verdict('cc', 'exact')).toBeNull()
  })

  it('a response that lands after the host changed is discarded', async () => {
    const d = deferred<Response>()
    vi.spyOn(globalThis, 'fetch').mockReturnValue(d.promise)
    render(<ResumeTemplateSettings />)
    await act(async () => { fireEvent.click(testButton('cc', 'exact')) })

    fireEvent.change(screen.getByTestId('resume-template-host'), { target: { value: H2 } })
    await act(async () => {
      d.resolve(jsonResponse({ resolved: true, detail: 'from the other machine' }))
      await d.promise
    })

    expect(verdict('cc', 'exact')).toBeNull()
  })

  it('a response that lands after the row was edited is discarded', async () => {
    const d = deferred<Response>()
    vi.spyOn(globalThis, 'fetch').mockReturnValue(d.promise)
    render(<ResumeTemplateSettings />)
    await act(async () => { fireEvent.click(testButton('cc', 'exact')) })

    fireEvent.change(input('cc', 'exact'), { target: { value: 'cld-yolo --resume {id}' } })
    await act(async () => {
      d.resolve(jsonResponse({ resolved: true, detail: '/usr/bin/claude' }))
      await d.promise
    })

    expect(verdict('cc', 'exact')).toBeNull()
  })

  it('a verdict from a superseded request never overwrites a newer one', async () => {
    // Away and back leaves the host and the word EXACTLY as the first request
    // sent them, so the (host, word) pair cannot tell the two requests apart.
    // Only the request's own identity can.
    const first = deferred<Response>()
    const second = deferred<Response>()
    vi.spyOn(globalThis, 'fetch')
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise)
    render(<ResumeTemplateSettings />)
    await act(async () => { fireEvent.click(testButton('cc', 'exact')) })

    fireEvent.change(screen.getByTestId('resume-template-host'), { target: { value: H2 } })
    fireEvent.change(screen.getByTestId('resume-template-host'), { target: { value: H1 } })
    await act(async () => { fireEvent.click(testButton('cc', 'exact')) })

    // Reverse order: the newer answer lands first, the abandoned one after.
    await act(async () => {
      second.resolve(jsonResponse({ resolved: false, reason: 'not_found' }))
      await second.promise
    })
    await act(async () => {
      first.resolve(jsonResponse({ resolved: true, detail: 'from the abandoned request' }))
      await first.promise
    })

    const el = verdict('cc', 'exact')!
    expect(el.getAttribute('data-status')).toBe('unresolved')
    expect(el.textContent).not.toContain('abandoned')
  })

  it('editing a row cancels its in-flight request, even if the word is retyped', async () => {
    // The stated contract is "editing the row invalidates its verdict —
    // including one still in flight". Retyping the original value restores the
    // word, but not the request the user abandoned by editing.
    const d = deferred<Response>()
    vi.spyOn(globalThis, 'fetch').mockReturnValue(d.promise)
    render(<ResumeTemplateSettings />)
    const original = input('cc', 'exact').value
    await act(async () => { fireEvent.click(testButton('cc', 'exact')) })

    fireEvent.change(input('cc', 'exact'), { target: { value: 'other --resume {id}' } })
    fireEvent.change(input('cc', 'exact'), { target: { value: original } })
    await act(async () => {
      d.resolve(jsonResponse({ resolved: true, detail: '/usr/bin/claude' }))
      await d.promise
    })

    expect(verdict('cc', 'exact')).toBeNull()
  })

  it('a verdict for one row does not leak onto the other row of the same agent', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ resolved: true, detail: 'x' }))
    render(<ResumeTemplateSettings />)
    await act(async () => { fireEvent.click(testButton('cc', 'exact')) })
    expect(verdict('cc', 'exact')).toBeTruthy()
    expect(verdict('cc', 'fallback')).toBeNull()
  })

  it('a pending row disables its own Test button until the verdict lands', async () => {
    const d = deferred<Response>()
    vi.spyOn(globalThis, 'fetch').mockReturnValue(d.promise)
    render(<ResumeTemplateSettings />)
    await act(async () => { fireEvent.click(testButton('cc', 'exact')) })

    expect(testButton('cc', 'exact')).toBeDisabled()
    expect(testButton('cc', 'fallback')).not.toBeDisabled()

    await act(async () => { d.resolve(jsonResponse({ resolved: true, detail: 'x' })); await d.promise })
    await waitFor(() => expect(testButton('cc', 'exact')).not.toBeDisabled())
  })
})

describe('ResumeTemplateSettings — a draft is uncommitted state only', () => {
  it('a committed row follows a later store change instead of pinning the saved value', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ resolved: true, detail: 'x' }))
    render(<ResumeTemplateSettings />)
    const el = input('cc', 'exact')
    fireEvent.change(el, { target: { value: 'cld-yolo --resume {id}' } })
    fireEvent.keyDown(el, { key: 'Enter' })
    expect(input('cc', 'exact').value).toBe('cld-yolo --resume {id}')

    // Another window writes the same row, and `syncManager` lands it here.
    act(() => {
      useResumeTemplateStore.getState().setTemplate('cc', 'exact', 'other-wrapper --resume {id}')
    })

    // A committed row has nothing left to protect: the store is the value.
    expect(input('cc', 'exact').value).toBe('other-wrapper --resume {id}')
    // And Test judges what a rebuild would actually run, not what this window
    // last typed.
    await act(async () => { fireEvent.click(testButton('cc', 'exact')) })
    expect(lastFetchBody()).toEqual({ command: 'other-wrapper' })
  })

  it('a committed row follows a reset performed elsewhere', () => {
    render(<ResumeTemplateSettings />)
    const el = input('cc', 'fallback')
    fireEvent.change(el, { target: { value: 'cld-yolo -c' } })
    fireEvent.blur(el)
    expect(input('cc', 'fallback').value).toBe('cld-yolo -c')

    act(() => { useResumeTemplateStore.getState().resetAgent('cc') })
    expect(input('cc', 'fallback').value).toBe(DEFAULT_RESUME_TEMPLATES.cc.fallback)
  })

  it('an edit that has NOT been committed still wins over a store change', () => {
    // The other side of the same rule: a draft exists to protect what the user
    // is still typing, and only that.
    render(<ResumeTemplateSettings />)
    fireEvent.change(input('cc', 'exact'), { target: { value: 'half-typed' } })

    act(() => {
      useResumeTemplateStore.getState().setTemplate('cc', 'exact', 'from-elsewhere {id}')
    })
    expect(input('cc', 'exact').value).toBe('half-typed')
  })
})

describe('ResumeTemplateSettings — i18n and the limits copy', () => {
  const originalT = useI18nStore.getState().t
  afterEach(() => { useI18nStore.setState({ t: originalT }) })

  it('states both limits: templates are global, and the test only approximates the pane', () => {
    render(<ResumeTemplateSettings />)
    const limits = screen.getByTestId('resume-template-limits').textContent ?? ''
    expect(limits).toContain(en['resume_template.limit_global'])
    expect(limits).toContain(en['resume_template.limit_probe'])
  })

  it('every key the component uses exists in BOTH en and zh-TW', () => {
    const keys = [...componentSource.matchAll(/'(resume_template\.[a-z_.]+)'/g)].map((m) => m[1])
    expect(keys.length).toBeGreaterThan(8)
    for (const key of new Set(keys)) {
      expect(en, `en.json missing ${key}`).toHaveProperty(key)
      expect(zhTW, `zh-TW.json missing ${key}`).toHaveProperty(key)
    }
  })

  it('renders no literal English — every string is a translation, an agent name or a host name', () => {
    // With `t` echoing its key, anything left that is not a known dynamic value
    // is hardcoded copy.
    useI18nStore.setState({ t: (key: string) => `«${key}»` })
    const { container } = render(<ResumeTemplateSettings />)

    const allowed = new Set(['Claude Code', 'Codex', 'OpenCode', 'mlab', 'air'])
    const walker = document.createTreeWalker(container, NodeFilter.SHOW_TEXT)
    const offenders: string[] = []
    for (let n = walker.nextNode(); n; n = walker.nextNode()) {
      const text = (n.textContent ?? '').trim()
      if (!text) continue
      if (text.startsWith('«') || allowed.has(text)) continue
      offenders.push(text)
    }
    expect(offenders).toEqual([])
  })
})
