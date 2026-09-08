import { describe, it, expect, beforeEach } from 'vitest'
import { useResumeTemplateStore, DEFAULT_RESUME_TEMPLATES } from './useResumeTemplateStore'
import { STORAGE_KEYS } from '../lib/storage/keys'

const store = () => useResumeTemplateStore.getState()

beforeEach(() => {
  useResumeTemplateStore.setState({ agents: {} })
  localStorage.clear()
})

describe('useResumeTemplateStore — defaults', () => {
  it('reproduces the shipped command shapes for the three known agents', () => {
    expect(store().getTemplates('cc')).toEqual({ exact: 'claude --resume {id}', fallback: 'claude -c' })
    expect(store().getTemplates('codex')).toEqual({ exact: 'codex resume {id}', fallback: 'codex resume --last' })
    expect(store().getTemplates('opencode')).toEqual({ exact: 'opencode -s {id}', fallback: 'opencode -c' })
  })

  it('has no template for an agent it does not know', () => {
    expect(store().getTemplates('aider')).toBeUndefined()
    expect(store().getTemplates('')).toBeUndefined()
  })

  it('does not answer for an inherited Object property name', () => {
    expect(store().getTemplates('constructor')).toBeUndefined()
    expect(store().getTemplates('__proto__')).toBeUndefined()
  })

  it('exposes the defaults as a frozen table nothing can edit in place', () => {
    expect(() => {
      ;(DEFAULT_RESUME_TEMPLATES as Record<string, { exact: string }>)['cc'].exact = 'nope'
    }).toThrow()
    expect(store().getTemplates('cc')?.exact).toBe('claude --resume {id}')
  })
})

describe('useResumeTemplateStore — editing', () => {
  it('stores one edited field and keeps the default for the other', () => {
    store().setTemplate('cc', 'exact', 'cld-yolo --resume {id}')
    expect(store().getTemplates('cc')).toEqual({ exact: 'cld-yolo --resume {id}', fallback: 'claude -c' })
  })

  it('stays sparse — only a customised agent gets a record', () => {
    store().setTemplate('codex', 'fallback', 'codex resume --last --yolo')
    expect(Object.keys(useResumeTemplateStore.getState().agents)).toEqual(['codex'])
  })

  it('lets a user teach it an agent it had no default for', () => {
    store().setTemplate('aider', 'exact', 'aider --restore {id}')
    expect(store().getTemplates('aider')).toEqual({ exact: 'aider --restore {id}', fallback: '' })
  })

  it('resetAgent drops the record so the default answers again', () => {
    store().setTemplate('cc', 'exact', 'cld-yolo --resume {id}')
    store().resetAgent('cc')
    expect(useResumeTemplateStore.getState().agents['cc']).toBeUndefined()
    expect(store().getTemplates('cc')).toEqual({ exact: 'claude --resume {id}', fallback: 'claude -c' })
  })

  it('resetAgent on an agent that was never customised changes nothing', () => {
    const before = useResumeTemplateStore.getState().agents
    store().resetAgent('cc')
    expect(useResumeTemplateStore.getState().agents).toEqual(before)
  })
})

describe('useResumeTemplateStore — persistence', () => {
  it('persists only the sparse agents map, under its own storage key', () => {
    store().setTemplate('cc', 'fallback', 'cld-yolo -c')
    const raw = localStorage.getItem(STORAGE_KEYS.RESUME_TEMPLATES)
    expect(raw).toBeTruthy()
    expect(JSON.parse(raw!).state).toEqual({
      agents: { cc: { exact: 'claude --resume {id}', fallback: 'cld-yolo -c' } },
    })
  })
})
