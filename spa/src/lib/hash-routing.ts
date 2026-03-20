// spa/src/lib/hash-routing.ts — v1 hash routing: #/tab/{tabId}

export function parseHash(): { tabId: string | null } {
  const hash = window.location.hash.replace(/^#\/?/, '')
  if (!hash) return { tabId: null }
  const parts = hash.split('/')
  if (parts[0] === 'tab' && parts[1]) return { tabId: parts[1] }
  return { tabId: null }
}

export function setHash(tabId: string) {
  const newHash = `#/tab/${tabId}`
  if (window.location.hash !== newHash) {
    window.location.hash = newHash
  }
}
