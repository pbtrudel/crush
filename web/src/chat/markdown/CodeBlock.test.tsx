import { render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import CodeBlock from './CodeBlock'

describe('CodeBlock', () => {
  it('highlights a known language with classes and no style attribute', async () => {
    const { container } = render(<CodeBlock code={'package main\n\nfunc main() {}'} language="go" />)
    await waitFor(() => expect(container.querySelector('[class*="tok-"]')).not.toBeNull(), { timeout: 10_000 })
    expect(container.querySelector('[style]')).toBeNull()
    expect(container.querySelector('pre')?.textContent).toContain('func main() {}')
  })

  it('renders an unknown label as plain text', async () => {
    const { container } = render(<CodeBlock code="x = 1" language="constructor" />)
    expect(container.querySelector('pre')?.textContent).toBe('x = 1')
    await new Promise((resolve) => setTimeout(resolve, 50))
    expect(container.querySelector('[class*="tok-"]')).toBeNull()
  })

  it('numbers the lines from lineStart', () => {
    const { container } = render(<CodeBlock code={'a\nb'} lineNumbers lineStart={41} />)
    expect([...container.querySelectorAll('[aria-hidden="true"]')].map((node) => node.textContent)).toEqual(['41', '42'])
  })

  it('copies the code', async () => {
    const writeText = vi.fn(async () => undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    render(<CodeBlock code="go test ./..." language="sh" />)
    screen.getByRole('button', { name: 'Copy code' }).click()
    await waitFor(() => expect(writeText).toHaveBeenCalledWith('go test ./...'))
    expect(await screen.findByText('Copied')).toBeInTheDocument()
  })
})
