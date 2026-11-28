import type { DiffLine } from './diff'

// Diffed is one edit's diff: the rows to draw, or the verdict that neither
// the file's two versions nor the call's own strings could be diffed.
export interface Diffed {
  lines: DiffLine[]
  // gaveUp is true when neither the file's two versions nor the call's own
  // strings could be diffed, so the view has to show the text instead.
  gaveUp: boolean
}

// DiffPair is the two versions of one file, or one edit's old and new
// string.
export type DiffPair = readonly [string, string]

// DiffCache answers an edit's diff by call id. The virtualized log unmounts
// a row that scrolls away and mounts it again, and the second mount asks
// for the same diff; a refused file pair is the expensive one (up to about
// a third of a second), and it is evicted from the diff module's single
// slot by the fallback that follows it.
export interface DiffCache {
  editDiff(
    callId: string,
    filePair: DiffPair | null,
    callPairs: readonly DiffPair[],
    compute: () => Diffed,
  ): Diffed
}

// maxCachedDiffs bounds what the cache keeps: one diff per call, and only
// the most recent ones. Every entry is bounded by the diff module's input
// guard, and a row that scrolled far enough out of reach costs one more
// diff when it comes back.
export const maxCachedDiffs = 32

interface CachedDiff {
  filePair: DiffPair | null
  callPairs: readonly DiffPair[]
  answer: Diffed
}

function samePair(a: DiffPair | null, b: DiffPair | null): boolean {
  return a === b || (a !== null && b !== null && a[0] === b[0] && a[1] === b[1])
}

function samePairs(a: readonly DiffPair[], b: readonly DiffPair[]): boolean {
  return a.length === b.length && a.every((pair, index) => samePair(pair, b[index]))
}

// createDiffCache builds one session's cache. The provider makes a new one
// when the selected session changes, so an answer never outlives the
// session it was computed for.
export function createDiffCache(limit = maxCachedDiffs): DiffCache {
  const cache = new Map<string, CachedDiff>()
  return {
    editDiff(callId, filePair, callPairs, compute) {
      const cached = cache.get(callId)
      if (
        cached !== undefined &&
        samePair(cached.filePair, filePair) &&
        samePairs(cached.callPairs, callPairs)
      ) {
        return cached.answer
      }
      const answer = compute()
      // Re-inserting keeps the map in the order the answers were computed,
      // so the oldest one is the first to go.
      cache.delete(callId)
      cache.set(callId, { filePair, callPairs, answer })
      while (cache.size > limit) {
        const oldest = cache.keys().next()
        if (oldest.done) {
          break
        }
        cache.delete(oldest.value)
      }
      return answer
    },
  }
}
