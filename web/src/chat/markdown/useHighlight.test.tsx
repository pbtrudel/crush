import { act, render, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

// The tokenizer is mocked so a pass is a call to count, not a grammar
// load: these cases are about when a pass runs and how big a block may be.
const highlighter = vi.hoisted(() => ({ tokenize: vi.fn() }))
vi.mock('./highlighter', () => ({ tokenize: highlighter.tokenize }))

import CodeBlock from './CodeBlock'
import { highlightThrottleMs, maxHighlightSource } from './useHighlight'

beforeEach(() => {
  highlighter.tokenize.mockReset()
  highlighter.tokenize.mockResolvedValue([[{ text: 'x', className: 'tok-keyword' }]])
})

afterEach(() => {
  vi.useRealTimers()
})

describe('useHighlight', () => {
  it('never tokenizes a block over the cap', async () => {
    vi.useFakeTimers()
    const { container } = render(<CodeBlock code={'x'.repeat(17 * 1024)} language="go" />)
    await act(async () => {
      await vi.advanceTimersByTimeAsync(highlightThrottleMs * 4)
    })
    expect(maxHighlightSource).toBe(16 * 1024)
    expect(highlighter.tokenize).not.toHaveBeenCalled()
    expect(container.querySelector('[class*="tok-"]')).toBeNull()
  })

  it('runs one pass for code that changes inside the throttle window', async () => {
    vi.useFakeTimers()
    const { rerender } = render(<CodeBlock code="a = 1" language="go" />)
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0)
    })
    expect(highlighter.tokenize.mock.calls.map((call) => call[0])).toEqual(['a = 1'])

    // Three changes 50 ms after the first pass, well inside 150 ms.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(50)
    })
    rerender(<CodeBlock code={'a = 1\n'} language="go" />)
    rerender(<CodeBlock code={'a = 1\nb'} language="go" />)
    rerender(<CodeBlock code={'a = 1\nb = 2'} language="go" />)
    expect(highlighter.tokenize).toHaveBeenCalledTimes(1)

    // The trailing pass covers the final text, once.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(highlightThrottleMs)
    })
    expect(highlighter.tokenize.mock.calls.map((call) => call[0])).toEqual(['a = 1', 'a = 1\nb = 2'])
  })

  // A streamed block grows after its last pass. Until the trailing pass
  // lands, the complete lines keep their highlight and the unfinished tail
  // renders plain; the trailing pass then covers the whole source.
  it('highlights a streamed block up to its last complete line', async () => {
    vi.useFakeTimers()
    highlighter.tokenize.mockImplementation(async (code: string) =>
      code.split('\n').map((line) => [{ text: line, className: 'tok-keyword' }]),
    )
    const { container, rerender } = render(<CodeBlock code={'a = 1\nb = 2\n'} language="go" />)
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0)
    })
    expect(highlighter.tokenize.mock.calls.map((call) => call[0])).toEqual(['a = 1\nb = 2\n'])
    // Two lines plus the empty line the trailing newline starts.
    expect(container.querySelectorAll('[class*="tok-"]')).toHaveLength(3)

    // Two deltas inside the throttle window: the first pass's last complete
    // line is final, so it stays highlighted and the new tail is plain.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(50)
    })
    rerender(<CodeBlock code={'a = 1\nb = 2\nc'} language="go" />)
    rerender(<CodeBlock code={'a = 1\nb = 2\nc = 3'} language="go" />)
    expect(highlighter.tokenize).toHaveBeenCalledTimes(1)
    expect(container.querySelectorAll('[class*="tok-"]')).toHaveLength(2)
    expect(container.querySelector('pre')?.textContent).toContain('c = 3')

    // The trailing pass covers the final source.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(highlightThrottleMs)
    })
    expect(highlighter.tokenize.mock.calls.map((call) => call[0])).toEqual([
      'a = 1\nb = 2\n',
      'a = 1\nb = 2\nc = 3',
    ])
    expect(container.querySelectorAll('[class*="tok-"]')).toHaveLength(3)
  })

  // A grammar that cannot load must leave the block plain rather than throw
  // or keep a stale pass: the pass is waited for, so the plain text is the
  // answer to a settled rejection and not a pass that has yet to run.
  it('leaves a block whose grammar load rejects plain', async () => {
    highlighter.tokenize.mockRejectedValue(new Error('grammar failed'))
    const { container } = render(<CodeBlock code="package main" language="go" />)
    await waitFor(() => expect(highlighter.tokenize).toHaveBeenCalledTimes(1))
    await act(async () => {
      await expect(highlighter.tokenize.mock.results[0]?.value).rejects.toThrow('grammar failed')
      // One more turn lets React settle the swallowed rejection.
      await Promise.resolve()
    })
    expect(container.querySelector('[class*="tok-"]')).toBeNull()
    expect(container.querySelector('pre')?.textContent).toBe('package main')
  })
})
