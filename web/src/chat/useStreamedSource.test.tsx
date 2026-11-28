import { act, renderHook } from '@testing-library/react'
import { StrictMode, type ReactNode } from 'react'
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

  it('keeps a stream moving across StrictMode\u2019s double mount', () => {
    vi.useFakeTimers()
    // StrictMode mounts, unmounts and mounts again, so the cleanup runs once
    // before the hook ever settles. A pending slot left behind by it would
    // stop every later pass and freeze the block.
    const wrapper = ({ children }: { children: ReactNode }) => <StrictMode>{children}</StrictMode>
    const { result, rerender } = renderHook(
      ({ source, streaming }: { source: string; streaming: boolean }) =>
        useStreamedSource(source, streaming),
      { wrapper, initialProps: { source: 'a', streaming: true } },
    )

    rerender({ source: 'ab', streaming: true })
    act(() => {
      vi.advanceTimersByTime(streamedSourceInterval)
    })
    rerender({ source: 'abc', streaming: true })
    act(() => {
      vi.advanceTimersByTime(streamedSourceInterval)
    })
    expect(result.current).toBe('abc')
  })

  it('starts a stream from the text the block had settled on', () => {
    vi.useFakeTimers()
    const { result, rerender } = renderHook(
      ({ source, streaming }: { source: string; streaming: boolean }) =>
        useStreamedSource(source, streaming),
      { initialProps: { source: 'a', streaming: false } },
    )
    // The block settles on new text while it is idle: a stream that starts
    // afterwards must not fall back to the snapshot it was mounted with.
    rerender({ source: 'ab', streaming: false })
    expect(result.current).toBe('ab')
    rerender({ source: 'abc', streaming: true })
    expect(result.current).toBe('ab')
    act(() => {
      vi.advanceTimersByTime(streamedSourceInterval)
    })
    expect(result.current).toBe('abc')
  })
})
