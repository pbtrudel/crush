import { describe, expect, it } from 'vitest'

import { safeHref, safeProtocols } from './links'

describe('safeHref', () => {
  // Ruling D1: the allowed set is exactly these three protocols.
  it('allows exactly the web and mail protocols', () => {
    expect([...safeProtocols]).toEqual(['http', 'https', 'mailto'])
  })

  // The URL parser canonicalises, so a bare origin gains its slash.
  it('keeps absolute web and mail links', () => {
    expect(safeHref('https://charm.land')).toBe('https://charm.land/')
    expect(safeHref('http://localhost:8080/x?y=1')).toBe('http://localhost:8080/x?y=1')
    expect(safeHref('mailto:me@example.com')).toBe('mailto:me@example.com')
  })

  // A sanitizer that HTML-encodes the address would turn this query into
  // `?a=1&amp;b=2`, which is a different query.
  it('keeps a query string intact', () => {
    expect(safeHref('https://x.com/?a=1&b=2')).toBe('https://x.com/?a=1&b=2')
    expect(safeHref('https://x.com/search?q=a%20b&page=2')).toBe('https://x.com/search?q=a%20b&page=2')
  })

  it('keeps a mailto query intact', () => {
    expect(safeHref('mailto:me@example.com?subject=a&body=b')).toBe('mailto:me@example.com?subject=a&body=b')
  })

  it('canonicalises the address it returns', () => {
    expect(safeHref('HTTPS://Charm.Land/Path')).toBe('https://charm.land/Path')
    expect(safeHref('  https://charm.land  ')).toBe('https://charm.land/')
  })

  it('refuses script, data and relative links', () => {
    expect(safeHref('javascript:alert(1)')).toBeNull()
    expect(safeHref(' JavaScript:alert(1)')).toBeNull()
    expect(safeHref('data:text/html,<b>x</b>')).toBeNull()
    expect(safeHref('/api/sessions')).toBeNull()
    expect(safeHref('parser.go')).toBeNull()
    expect(safeHref('//evil.com')).toBeNull()
    expect(safeHref('file:///etc/passwd')).toBeNull()
    expect(safeHref('https://')).toBeNull()
  })
})
