import { act, render } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

// The tokenizer is mocked so a pass is a call to count, not a grammar
// load: these cases are about when a pass runs and how big a block may be,
// and fail is the chunk import that rejects.
const highlighter = vi.hoisted(() => ({ fail: false, tokenize: vi.fn() }))
vi.mock('./highlighter', () => {
  if (highlighter.fail) {
    throw new Error('chunk failed')
  }
  return { tokenize: highlighter.tokenize }
})

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

  it('leaves a block whose tokenize rejects plain', async () => {
    highlighter.tokenize.mockRejectedValue(new Error('grammar failed'))
    const { container } = render(<CodeBlock code="package main" language="go" />)
    await act(async () => {
      await Promise.resolve()
    })
    expect(container.querySelector('[class*="tok-"]')).toBeNull()
    expect(container.querySelector('pre')?.textContent).toBe('package main')
  })
})
