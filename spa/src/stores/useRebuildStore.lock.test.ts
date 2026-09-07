// spa/src/stores/useRebuildStore.lock.test.ts — the shared operation lock (spec §4.11).
//
// The lock is an OUTERMOST-ACQUIRE TOKEN model, not a per-call counter:
// `acquireOperationLock(owner)` hands back a token when it grants and `null`
// when a different owner holds it. A nested acquire by the SAME owner gets a
// re-entry token, and releasing that one is a no-op — only the outermost token
// can drop the lock. That is what lets `undoLastRestore` → `restoreAll` nest
// without the inner call unlocking the world underneath its caller.
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { useRebuildStore, withOperationLock, type OperationLockToken } from './useRebuildStore'

const acquire = (owner: string) => useRebuildStore.getState().acquireOperationLock(owner)
const release = (token: OperationLockToken) => useRebuildStore.getState().releaseOperationLock(token)
const held = () => useRebuildStore.getState().lockedBy

describe('operation lock', () => {
  beforeEach(() => useRebuildStore.setState({ operations: {}, lockedBy: null }))

  it('grants to the first caller and refuses the second', () => {
    expect(acquire('rebuild:p1')).not.toBeNull()
    expect(acquire('snapshot:restoreAll')).toBeNull()
  })

  it('is re-entrant for the same owner (undo → restoreAll)', () => {
    const outer = acquire('snapshot:undo')
    const inner = acquire('snapshot:undo')
    expect(outer).toMatchObject({ owner: 'snapshot:undo', outermost: true })
    expect(inner).toMatchObject({ owner: 'snapshot:undo', outermost: false })
  })

  it('releases only for the holder', () => {
    const token = acquire('rebuild:p1')!
    // A token that was never granted (forged, or left over from an earlier
    // holder) must not be able to unlock someone else's operation.
    release({ owner: 'snapshot:restoreAll', outermost: true })
    expect(held()).toBe('rebuild:p1')
    release(token)
    expect(held()).toBeNull()
  })

  it('blocks a legacy snapshot action while a rebuild holds the lock', () => {
    acquire('rebuild:p1')
    expect(acquire('snapshot:restoreAll')).toBeNull()
  })

  it('nested undo → restoreAll keeps the lock until the outermost release', () => {
    const outer = acquire('snapshot:undo')
    const inner = acquire('snapshot:undo')
    release(inner!)
    expect(held()).toBe('snapshot:undo')
    release(outer!)
    expect(held()).toBeNull()
  })

  it('hands the lock on to the next owner once it is released', () => {
    const first = acquire('rebuild:p1')!
    release(first)
    expect(acquire('snapshot:restoreAll')).not.toBeNull()
    expect(held()).toBe('snapshot:restoreAll')
  })
})

describe('withOperationLock', () => {
  beforeEach(() => useRebuildStore.setState({ operations: {}, lockedBy: null }))

  it('releases the lock when the body resolves', async () => {
    const out = await withOperationLock('rebuild:p1', async () => 'done', () => 'refused')
    expect(out).toBe('done')
    expect(held()).toBeNull()
  })

  it('releases the lock when the body throws', async () => {
    await expect(
      withOperationLock('rebuild:p1', async () => { throw new Error('boom') }, () => 'refused'),
    ).rejects.toThrow('boom')
    expect(held()).toBeNull()
  })

  it('runs the refusal path and names the holder when another owner has the lock', async () => {
    acquire('rebuild:p1')
    const body = vi.fn()
    const out = await withOperationLock('snapshot:restoreAll', async () => { body(); return 'done' }, (holder) => `refused:${holder}`)
    expect(body).not.toHaveBeenCalled()
    expect(out).toBe('refused:rebuild:p1')
    // The refused caller must NOT have dropped the real holder's lock.
    expect(held()).toBe('rebuild:p1')
  })

  it('a nested same-owner body leaves the lock held for the outer body', async () => {
    const seen: (string | null)[] = []
    await withOperationLock('snapshot:undo', async () => {
      await withOperationLock('snapshot:undo', async () => { seen.push(held()); return 0 }, () => -1)
      seen.push(held())
      return 0
    }, () => -1)
    seen.push(held())
    expect(seen).toEqual(['snapshot:undo', 'snapshot:undo', null])
  })
})
