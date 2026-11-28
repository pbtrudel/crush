import { render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import MathFormula from './Math'
import { maxMathSource } from './mathml'

describe('MathFormula', () => {
  it('renders TeX as MathML in the MathML namespace', async () => {
    const { container } = render(<MathFormula tex={'\\frac{1}{3}'} display />)
    await waitFor(() => expect(container.querySelector('math')).not.toBeNull(), { timeout: 10_000 })
    const math = container.querySelector('math')
    expect(math?.namespaceURI).toBe('http://www.w3.org/1998/Math/MathML')
    expect(math?.getAttribute('display')).toBe('block')
    expect(container.querySelector('[style]')).toBeNull()
  })

  it('shows the source of TeX that does not parse', async () => {
    const { container } = render(<MathFormula tex={'\\frac{1}{'} display={false} />)
    await screen.findByText('\\frac{1}{')
    expect(container.querySelector('math')).toBeNull()
  })

  // The lazy chunk is awaited first, so a missing <math> below means the
  // cap held rather than the chunk not having arrived yet.
  it('shows the source of TeX past the size cap', async () => {
    await import('./MathView')
    const tex = 'x'.repeat(maxMathSource + 1)
    const { container } = render(<MathFormula tex={tex} display={false} />)
    expect(screen.getByText(tex)).toBeInTheDocument()
    await new Promise((resolve) => setTimeout(resolve, 50))
    expect(container.querySelector('math')).toBeNull()
    expect(container.textContent).toBe(tex)
  })
})
