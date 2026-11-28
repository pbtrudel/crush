import { act, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import CopyButton from './CopyButton'

function clipboard(writeText: (text: string) => Promise<void>) {
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
}

afterEach(() => {
  vi.useRealTimers()
})

describe('CopyButton', () => {
  it('says Copied after a good copy, then Copy again', async () => {
    vi.useFakeTimers()
    const writeText = vi.fn(async () => undefined)
    clipboard(writeText)
    render(<CopyButton text="go test ./..." label="Copy code" />)
    await act(async () => {
      screen.getByRole('button', { name: 'Copy code' }).click()
    })
    expect(screen.getByRole('button', { name: 'Copy code' })).toHaveTextContent(/^Copied$/)
    act(() => {
      vi.advanceTimersByTime(1500)
    })
    expect(screen.getByRole('button', { name: 'Copy code' })).toHaveTextContent(/^Copy$/)
  })

  // A denied clipboard must say so rather than look like a no-op click.
  it('says Copy failed after a rejected copy, then Copy again', async () => {
    vi.useFakeTimers()
    const writeText = vi.fn(async () => {
      throw new Error('denied')
    })
    clipboard(writeText)
    render(<CopyButton text="go test ./..." label="Copy code" />)
    await act(async () => {
      screen.getByRole('button', { name: 'Copy code' }).click()
    })
    expect(screen.getByRole('button', { name: 'Copy code' })).toHaveTextContent(/^Copy failed$/)
    act(() => {
      vi.advanceTimersByTime(1500)
    })
    expect(screen.getByRole('button', { name: 'Copy code' })).toHaveTextContent(/^Copy$/)
  })
})
