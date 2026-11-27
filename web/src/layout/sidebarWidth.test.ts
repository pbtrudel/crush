import { describe, expect, it } from 'vitest'

import { clampSidebarWidth, sidebarDefaultWidth } from './sidebarWidth'

describe('clampSidebarWidth', () => {
  it('keeps the default', () => {
    expect(sidebarDefaultWidth).toBe(280)
    expect(clampSidebarWidth(sidebarDefaultWidth)).toBe(280)
  })

  it('clamps to the 264 to 420 range', () => {
    expect(clampSidebarWidth(100)).toBe(264)
    expect(clampSidebarWidth(700)).toBe(420)
    expect(clampSidebarWidth(360)).toBe(360)
  })

  it('rounds a fractional drag', () => {
    expect(clampSidebarWidth(300.7)).toBe(301)
  })
})
