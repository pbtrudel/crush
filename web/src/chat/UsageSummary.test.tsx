import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import UsageSummary from './UsageSummary'

describe('UsageSummary', () => {
  it('shows the context in use, its share of the window and the cost', () => {
    const { container } = render(
      <UsageSummary usage={{ promptTokens: 42000, completionTokens: 1800, cost: 0.1234, contextWindow: 200000 }} />,
    )
    const summary = screen.getByTestId('usage-summary')
    expect(summary).toHaveTextContent('43.8K tokens')
    expect(summary).toHaveTextContent('22% of 200K')
    expect(summary).toHaveTextContent('$0.12')
    expect(screen.getByRole('img', { name: '22% of the context window used' })).toBeInTheDocument()
    expect(container.querySelector('[style]')).toBeNull()
  })

  it('drops the ring when the window is unknown', () => {
    render(<UsageSummary usage={{ promptTokens: 10, completionTokens: 0, cost: 0, contextWindow: 0 }} />)
    expect(screen.queryByRole('img')).toBeNull()
    expect(screen.getByTestId('usage-summary')).toHaveTextContent('10 tokens')
  })

  it('renders nothing for a session that has used nothing', () => {
    const { container } = render(
      <UsageSummary usage={{ promptTokens: 0, completionTokens: 0, cost: 0, contextWindow: 1000 }} />,
    )
    expect(container).toBeEmptyDOMElement()
  })

  // A context larger than its window is full, not negative or over 100.
  it('clamps a context that overflowed its window', () => {
    render(<UsageSummary usage={{ promptTokens: 300000, completionTokens: 0, cost: 0, contextWindow: 200000 }} />)
    expect(screen.getByTestId('usage-summary')).toHaveTextContent('100% of 200K')
    expect(screen.getByRole('img', { name: '100% of the context window used' })).toBeInTheDocument()
  })

  // The window and the counts come from the wire, and a session with no
  // measurement carries nothing: no NaN, no Infinity, no ring.
  it('never writes NaN or Infinity', () => {
    const { container } = render(
      <UsageSummary
        usage={{ promptTokens: 10, completionTokens: 0, cost: 0, contextWindow: Number.POSITIVE_INFINITY }}
      />,
    )
    expect(container.textContent).not.toMatch(/NaN|Infinity/)
    expect(screen.queryByRole('img')).toBeNull()

    const { container: empty } = render(
      <UsageSummary usage={{ promptTokens: Number.NaN, completionTokens: 0, cost: 0, contextWindow: 1000 }} />,
    )
    expect(empty).toBeEmptyDOMElement()
  })
})
