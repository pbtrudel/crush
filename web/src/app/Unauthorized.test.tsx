import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import Unauthorized from './Unauthorized'

describe('Unauthorized', () => {
  it('points at the terminal command that makes a new link', () => {
    render(<Unauthorized />)
    expect(screen.getByText(/no valid token/i)).toBeInTheDocument()
    expect(screen.getByText('/web')).toBeInTheDocument()
  })
})
