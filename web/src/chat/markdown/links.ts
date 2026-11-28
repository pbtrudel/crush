// safeProtocols is the whole set a rendered link may carry.
export const safeProtocols = ['http', 'https', 'mailto'] as const

// absolute is the grammar a link must wear before the URL parser sees it:
// a protocol-relative //evil.com and a relative path both stop here.
const absolute = /^(https?:\/\/|mailto:)/i

// safeHref answers the address a rendered link may carry, or null. Only
// absolute web and mail links pass: a relative link would navigate the
// gateway's own pages, and every other scheme (javascript:, data:, file:)
// is refused. The URL parser canonicalises the address and percent-encodes
// what a URL must not hold; it does not HTML-encode, so a query string
// keeps its amps, and the href the element gets is the address as written.
export function safeHref(url: string): string | null {
  const trimmed = url.trim()
  if (!absolute.test(trimmed)) {
    return null
  }
  let parsed: URL
  try {
    parsed = new URL(trimmed)
  } catch {
    return null
  }
  const protocol = parsed.protocol.replace(/:$/, '')
  if (!(safeProtocols as readonly string[]).includes(protocol)) {
    return null
  }
  return parsed.href
}
