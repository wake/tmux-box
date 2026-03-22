import type { Tab } from '../types/tab'

export function getSessionName(tab: Tab): string | undefined {
  return tab.data.sessionName as string | undefined
}

export function getSessionCode(tab: Tab): string | undefined {
  return tab.data.sessionCode as string | undefined
}

export function getFilePath(tab: Tab): string | undefined {
  return tab.data.filePath as string | undefined
}

export function isDirty(tab: Tab): boolean {
  return tab.data.isDirty === true
}
