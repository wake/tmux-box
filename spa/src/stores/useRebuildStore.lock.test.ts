// spa/src/stores/useRebuildStore.lock.test.ts — the shared operation lock (spec §4.11).
//
// The lock is a GRANT-IDENTITY model, not an owner-name model:
// `acquireOperationLock(owner)` grants only when nothing holds the lock, and
// hands back a grant. Nesting is not inferred from the owner string — two
// independent `runBatchRebuild` calls share the name `rebuild:batch` and must
// NOT be allowed to interleave — it has to be asked for, by passing the grant
// the caller is already holding. Releasing a nested grant is a no-op, and only
// the grant that actually took the lock can drop it. That is what lets
// `undoLastRestore` → `restoreAll` nest without the inner call unlocking the
// world underneath its caller.
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { useRebuildStore, withOperationLock, type OperationLockGrant } from './useRebuildStore'

const acquire = (owner: string, parent?: OperationLockGrant | null) =>
  useRebuildStore.getState().acquireOperationLock(owner, parent)
const release = (grant: OperationLockGrant) => useRebuildStore.getState().releaseOperationLock(grant)
const held = () => useRebuildStore.getState().lockedBy

describe('operation lock', () => {
  beforeEach(() => useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null }))

  it('grants to the first caller and refuses the second', () => {
    expect(acquire('rebuild:p1')).not.toBeNull()
    expect(acquire('snapshot:restoreAll')).toBeNull()
  })

  it('refuses a second top-level acquire even when the owner name matches', () => {
    // Two independent `runBatchRebuild` calls both use `rebuild:batch`. Owner
    // equality is not identity, and admitting the second on the strength of
    // the name alone lets them interleave.
    expect(acquire('rebuild:batch')).not.toBeNull()
    expect(acquire('rebuild:batch')).toBeNull()
  })

  it('nests only for a caller that passes the grant it already holds', () => {
    const outer = acquire('snapshot:undo')!
    expect(outer).toMatchObject({ owner: 'snapshot:undo', outermost: true })
    // Without the grant, even the same owner name is refused.
    expect(acquire('snapshot:restoreAll')).toBeNull()
    const inner = acquire('snapshot:restoreAll', outer)
    expect(inner).toMatchObject({ owner: 'snapshot:restoreAll', outermost: false })
  })

  it('refuses a nested acquire whose grant is not the current one', () => {
    const stale = acquire('rebuild:batch')!
    release(stale)
    acquire('snapshot:restoreAll')
    expect(acquire('rebuild:p1', stale)).toBeNull()
    expect(held()).toBe('snapshot:restoreAll')
  })

  it('releases only for the grant that took the lock', () => {
    const grant = acquire('rebuild:p1')!
    // A grant that was never issued must not be able to unlock someone else's
    // operation, however convincing its owner name is.
    release({ ...grant, owner: 'rebuild:p1', id: Symbol('forged') })
    expect(held()).toBe('rebuild:p1')
    release(grant)
    expect(held()).toBeNull()
  })

  it('cannot be released by an earlier grant of the same owner', () => {
    const first = acquire('rebuild:batch')!
    release(first)
    const second = acquire('rebuild:batch')!
    release(first)
    expect(held()).toBe('rebuild:batch')
    release(second)
    expect(held()).toBeNull()
  })

  it('blocks a legacy snapshot action while a rebuild holds the lock', () => {
    acquire('rebuild:p1')
    expect(acquire('snapshot:restoreAll')).toBeNull()
  })

  it('nested undo → restoreAll keeps the lock until the outermost release', () => {
    const outer = acquire('snapshot:undo')!
    const inner = acquire('snapshot:restoreAll', outer)!
    release(inner)
    expect(held()).toBe('snapshot:undo')
    release(outer)
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
  beforeEach(() => useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null }))

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

  it('a body that threads its grant down leaves the lock held for the outer body', async () => {
    const seen: (string | null)[] = []
    await withOperationLock('snapshot:undo', async (grant) => {
      await withOperationLock('snapshot:restoreAll', async () => { seen.push(held()); return 0 }, () => -1, grant)
      seen.push(held())
      return 0
    }, () => -1)
    seen.push(held())
    expect(seen).toEqual(['snapshot:undo', 'snapshot:undo', null])
  })

  it('a nested body that does NOT thread the grant is refused, lock intact', async () => {
    let inner: string | number = 0
    await withOperationLock('rebuild:batch', async () => {
      inner = await withOperationLock('rebuild:batch', async () => 'ran', (holder) => `refused:${holder}`)
      return 0
    }, () => -1)
    expect(inner).toBe('refused:rebuild:batch')
    expect(held()).toBeNull()
  })
})
