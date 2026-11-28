import { describe, expect, it } from 'vitest'

import { anchoredScrollTop, followDistance, isAtEnd, prependedCount } from './scroll'

describe('isAtEnd', () => {
  it('counts a few pixels of slack as the end', () => {
    expect(isAtEnd({ scrollTop: 400, scrollHeight: 1000, clientHeight: 600 })).toBe(true)
    expect(isAtEnd({ scrollTop: 400 - followDistance, scrollHeight: 1000, clientHeight: 600 })).toBe(
      true,
    )
    expect(isAtEnd({ scrollTop: 300, scrollHeight: 1000, clientHeight: 600 })).toBe(false)
  })
})

describe('prependedCount', () => {
  it('counts the rows put in front of the old first one', () => {
    expect(prependedCount(['m3', 'm4'], ['m1', 'm2', 'm3', 'm4'])).toBe(2)
  })

  it('answers zero for an append, a stream, a reset or a first render', () => {
    expect(prependedCount(['m3'], ['m3', 'm4'])).toBe(0)
    expect(prependedCount(['m3'], ['m3'])).toBe(0)
    expect(prependedCount(['m3'], ['x1', 'x2'])).toBe(0)
    expect(prependedCount([], ['m1'])).toBe(0)
  })
})

describe('anchoredScrollTop', () => {
  it('moves the view by as much as the anchor moved', () => {
    expect(anchoredScrollTop(1000, 60, 360)).toBe(1300)
    expect(anchoredScrollTop(1000, 60, 60)).toBe(1000)
  })
})
