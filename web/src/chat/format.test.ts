import { describe, expect, it } from 'vitest'

import { formatCost, formatDuration, formatTokens } from './format'

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

describe('formatCost', () => {
  it('writes dollars to the cent and marks a sub-cent cost', () => {
    expect(formatCost(0)).toBe('$0.00')
    expect(formatCost(0.004)).toBe('<$0.01')
    expect(formatCost(0.1234)).toBe('$0.12')
    expect(formatCost(12.5)).toBe('$12.50')
  })
})
