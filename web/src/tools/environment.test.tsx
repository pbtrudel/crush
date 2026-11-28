import { render } from '@testing-library/react'
import { useContext } from 'react'
import { describe, expect, it, vi } from 'vitest'

import type { Diffed } from './diffCache'
import { ToolEnvironmentContext, ToolEnvironmentProvider } from './environment'

const refused: Diffed = { lines: [], gaveUp: true }

// Probe asks the environment's cache for the same call on every render,
// which is what a remounted tool row does.
function probe(compute: () => Diffed) {
  function Probe() {
    const environment = useContext(ToolEnvironmentContext)
    environment.diffs.editDiff('tc-1', ['old', 'new'], [], compute)
    return null
  }
  return <Probe />
}

describe('ToolEnvironmentProvider', () => {
  it('starts a fresh diff cache when the session changes', () => {
    const compute = vi.fn(() => refused)
    const { rerender } = render(
      <ToolEnvironmentProvider sessionId="s1">{probe(compute)}</ToolEnvironmentProvider>,
    )
    expect(compute).toHaveBeenCalledOnce()

    // The same session keeps the answer for the row it unmounted.
    rerender(<ToolEnvironmentProvider sessionId="s1">{probe(compute)}</ToolEnvironmentProvider>)
    expect(compute).toHaveBeenCalledOnce()

    // A new session must not read the previous one's cache.
    rerender(<ToolEnvironmentProvider sessionId="s2">{probe(compute)}</ToolEnvironmentProvider>)
    expect(compute).toHaveBeenCalledTimes(2)
  })
})
