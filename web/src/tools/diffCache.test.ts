import { describe, expect, it, vi } from 'vitest'

import { createDiffCache, type Diffed } from './diffCache'

function answer(text: string): Diffed {
  return { lines: [{ kind: 'add', text, oldNumber: null, newNumber: 1 }], gaveUp: false }
}

function deferred(): Diffed {
  return { lines: [], gaveUp: true }
}

describe('createDiffCache', () => {
  it('answers a second request for the same call and inputs without computing', () => {
    const cache = createDiffCache()
    const compute = vi.fn(() => answer('one'))
    const filePair: [string, string] = ['old', 'new']

    expect(cache.editDiff('tc-1', filePair, [], compute)).toBe(compute.mock.results[0].value)
    expect(compute).toHaveBeenCalledOnce()

    // A remount builds new arrays and new strings, so the cache compares
    // the values and not the references.
    const again = cache.editDiff('tc-1', ['old', 'new'], [], compute)
    expect(again).toBe(compute.mock.results[0].value)
    expect(compute).toHaveBeenCalledOnce()
  })

  it('computes again when the inputs of the same call change', () => {
    const cache = createDiffCache()
    const compute = vi.fn(() => answer('one'))

    cache.editDiff('tc-1', null, [['a', 'b']], compute)
    cache.editDiff('tc-1', ['old', 'new'], [['a', 'b']], compute)
    cache.editDiff('tc-1', ['old', 'new'], [['a', 'c']], compute)
    cache.editDiff('tc-1', ['old', 'new'], [['a', 'b'], ['c', 'd']], compute)

    expect(compute).toHaveBeenCalledTimes(4)
  })

  it('keeps one answer per call id', () => {
    const cache = createDiffCache()
    const first = vi.fn(() => answer('one'))
    const second = vi.fn(() => answer('two'))

    cache.editDiff('tc-1', ['old', 'new'], [], first)
    cache.editDiff('tc-2', ['old', 'new'], [], second)
    cache.editDiff('tc-1', ['old', 'new'], [], first)

    expect(first).toHaveBeenCalledOnce()
    expect(second).toHaveBeenCalledOnce()
  })

  it('forgets the oldest answer past the limit', () => {
    const cache = createDiffCache(2)
    const compute = vi.fn(() => answer('one'))

    cache.editDiff('tc-1', ['a', 'b'], [], compute)
    cache.editDiff('tc-2', ['a', 'b'], [], compute)
    cache.editDiff('tc-3', ['a', 'b'], [], compute)
    cache.editDiff('tc-1', ['a', 'b'], [], compute)

    // tc-2 and tc-3 held the slots; tc-1 was evicted and computed again.
    expect(compute).toHaveBeenCalledTimes(4)
  })

  it('keeps a refused pair so a remount does not ask again', () => {
    const cache = createDiffCache()
    const compute = vi.fn(() => deferred())

    cache.editDiff('tc-1', ['huge', 'huge'], [], compute)
    expect(cache.editDiff('tc-1', ['huge', 'huge'], [], compute)).toEqual(deferred())
    expect(compute).toHaveBeenCalledOnce()
  })
})
