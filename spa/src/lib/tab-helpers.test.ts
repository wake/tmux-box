import { describe, it, expect } from 'vitest'
import { getSessionName, getFilePath, isDirty } from './tab-helpers'
import type { Tab } from '../types/tab'

const sessionTab: Tab = {
  id: 's1', type: 'session', label: 'dev', icon: 'Terminal', hostId: 'mlab',
  viewMode: 'terminal', data: { sessionName: 'dev-server' }, pinned: false, locked: false,
}

const editorTab: Tab = {
  id: 'e1', type: 'editor', label: 'file.ts', icon: 'File', hostId: 'mlab',
  data: { filePath: '/src/file.ts', isDirty: true }, pinned: false, locked: false,
}

const emptyTab: Tab = {
  id: 'x1', type: 'unknown', label: 'test', icon: 'X', hostId: 'mlab',
  data: {}, pinned: false, locked: false,
}

describe('getSessionName', () => {
  it('returns sessionName from session tab', () => {
    expect(getSessionName(sessionTab)).toBe('dev-server')
  })
  it('returns undefined from tab without sessionName', () => {
    expect(getSessionName(emptyTab)).toBeUndefined()
  })
})

describe('getFilePath', () => {
  it('returns filePath from editor tab', () => {
    expect(getFilePath(editorTab)).toBe('/src/file.ts')
  })
  it('returns undefined from tab without filePath', () => {
    expect(getFilePath(emptyTab)).toBeUndefined()
  })
})

describe('isDirty', () => {
  it('returns true for dirty editor tab', () => {
    expect(isDirty(editorTab)).toBe(true)
  })
  it('returns false for tab without isDirty', () => {
    expect(isDirty(emptyTab)).toBe(false)
  })
  it('returns false for explicitly clean tab', () => {
    expect(isDirty({ ...editorTab, data: { ...editorTab.data, isDirty: false } })).toBe(false)
  })
})
