import { render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import MarkdownBoundary from './MarkdownBoundary'

function Boom(): never {
  throw new Error('boom')
}

describe('MarkdownBoundary', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('shows the source as plain text when the tree throws', () => {
    // React prints the caught error; the test cares about the fallback.
    vi.spyOn(console, 'error').mockImplementation(() => undefined)
    const { container } = render(
      <MarkdownBoundary source="the source">
        <Boom />
      </MarkdownBoundary>,
    )
    expect(screen.getByText('the source')).toBeInTheDocument()
    expect(container.querySelector('pre')?.textContent).toBe('the source')
  })

  it('renders its children again when the source changes', () => {
    vi.spyOn(console, 'error').mockImplementation(() => undefined)
    const { rerender } = render(
      <MarkdownBoundary source="a">
        <Boom />
      </MarkdownBoundary>,
    )
    rerender(
      <MarkdownBoundary source="b">
        <p>recovered</p>
      </MarkdownBoundary>,
    )
    expect(screen.getByText('recovered')).toBeInTheDocument()
  })
})
