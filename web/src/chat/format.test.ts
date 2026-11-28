import { describe, expect, it } from 'vitest'

import { formatDuration, formatTokens } from './format'

describe('formatDuration', () => {
  it('writes seconds, then minutes and seconds', () => {
    expect(formatDuration(0)).toBe('0s')
    expect(formatDuration(42)).toBe('42s')
    expect(formatDuration(75)).toBe('1m 15s')
    expect(formatDuration(3600)).toBe('60m 0s')
    expect(formatDuration(-3)).toBe('0s')
  })
})

describe('formatTokens', () => {
  it('writes thousands and millions with one decimal', () => {
    expect(formatTokens(950)).toBe('950')
    expect(formatTokens(43800)).toBe('43.8K')
    expect(formatTokens(200000)).toBe('200K')
    expect(formatTokens(1250000)).toBe('1.3M')
  })
})
