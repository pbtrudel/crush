import { render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import Markdown from './Markdown'
import MarkdownBoundary from './MarkdownBoundary'

// A DOM renderer that throws: the rendered tree sits inside the boundary,
// so this reaches the fallback instead of unmounting the app.
vi.mock('./CodeBlock', () => ({
  default: () => {
    throw new Error('code block exploded')
  },
}))

// A renderer that throws while the tree is walked: safeHref runs inside
// children(...), so this one pins the boundary around the walk itself.
vi.mock('./links', () => ({
  safeHref: () => {
    throw new Error('link renderer exploded')
  },
  safeProtocols: ['http', 'https', 'mailto'],
}))

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

  it('catches a DOM renderer that throws for a node type', () => {
    vi.spyOn(console, 'error').mockImplementation(() => undefined)
    const source = '```go\nfunc main() {}\n```'
    const { container } = render(<Markdown source={source} />)
    expect(container.querySelector('pre')?.textContent).toBe(source)
  })

  it('catches a walk-time renderer that throws for a node type', () => {
    vi.spyOn(console, 'error').mockImplementation(() => undefined)
    const source = 'See [Charm](https://charm.land).'
    const { container } = render(<Markdown source={source} />)
    expect(container.querySelector('pre')?.textContent).toBe(source)
  })
})
