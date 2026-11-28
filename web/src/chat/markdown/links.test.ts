import { describe, expect, it } from 'vitest'

import { safeHref, safeProtocols } from './links'

describe('safeHref', () => {
  // Ruling D1: the allowed set is exactly these three protocols.
  it('allows exactly the web and mail protocols', () => {
    expect([...safeProtocols]).toEqual(['http', 'https', 'mailto'])
  })

  it('keeps absolute web and mail links', () => {
    expect(safeHref('https://charm.land')).toBe('https://charm.land')
    expect(safeHref('http://localhost:8080/x?y=1')).toBe('http://localhost:8080/x?y=1')
    expect(safeHref('mailto:me@example.com')).toBe('mailto:me@example.com')
  })

  it('refuses script, data and relative links', () => {
    expect(safeHref('javascript:alert(1)')).toBeNull()
    expect(safeHref(' JavaScript:alert(1)')).toBeNull()
    expect(safeHref('data:text/html,<b>x</b>')).toBeNull()
    expect(safeHref('/api/sessions')).toBeNull()
    expect(safeHref('parser.go')).toBeNull()
  })
})
