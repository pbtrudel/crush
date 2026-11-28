import { act, renderHook } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { streamedSourceInterval, useStreamedSource } from './useStreamedSource'

afterEach(() => {
  vi.useRealTimers()
})

function stream(initial: string) {
  return renderHook(
    ({ source, streaming }: { source: string; streaming: boolean }) =>
      useStreamedSource(source, streaming),
    { initialProps: { source: initial, streaming: true } },
  )
}

describe('useStreamedSource', () => {
  it('holds a streaming block back for one interval, then shows the last delta', () => {
    vi.useFakeTimers()
    const { result, rerender } = stream('One')

    rerender({ source: 'One two', streaming: true })
    rerender({ source: 'One two three', streaming: true })
    // Nothing has reached the renderer yet: one pass, one parse.
    expect(result.current).toBe('One')

    act(() => {
      vi.advanceTimersByTime(streamedSourceInterval)
    })
    // The deltas that arrived inside the interval cost nothing, and none of
    // them is lost.
    expect(result.current).toBe('One two three')
  })

  it('keeps one pass per interval under a stream of deltas', () => {
    vi.useFakeTimers()
    const { result, rerender } = stream('a')
    act(() => {
      vi.advanceTimersByTime(streamedSourceInterval)
    })
    expect(result.current).toBe('a')

    for (const source of ['ab', 'abc', 'abcd']) {
      rerender({ source, streaming: true })
      expect(result.current).toBe('a')
    }
    act(() => {
      vi.advanceTimersByTime(streamedSourceInterval / 2)
    })
    // Still inside the pass: a shorter wait must not cause a second parse.
    expect(result.current).toBe('a')
    act(() => {
      vi.advanceTimersByTime(streamedSourceInterval / 2)
    })
    expect(result.current).toBe('abcd')
  })

  it('answers a settled block at once', () => {
    vi.useFakeTimers()
    const { result, rerender } = stream('One')
    rerender({ source: 'One two', streaming: false })
    expect(result.current).toBe('One two')
    // The pending pass is dropped with the stream, so nothing lands late.
    act(() => {
      vi.advanceTimersByTime(streamedSourceInterval)
    })
    expect(result.current).toBe('One two')
  })

  it('cancels its timer when the block unmounts', () => {
    vi.useFakeTimers()
    const { unmount } = stream('One')
    unmount()
    expect(vi.getTimerCount()).toBe(0)
  })
})
