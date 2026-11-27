import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import App from './App'

describe('App', () => {
  it('shows the preview banner', () => {
    render(<App />)
    expect(screen.getByText('Preview: the new web UI is incomplete')).toBeInTheDocument()
  })
})
