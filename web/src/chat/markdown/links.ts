import { sanitizeUri } from 'micromark-util-sanitize-uri'

// safeProtocols is the whole set a rendered link may carry. Ruling D1 names
// this list; the published sanitizeUri (2.0.1) takes a RegExp rather than a
// list, so the pattern below is built from it.
export const safeProtocols = ['http', 'https', 'mailto'] as const

// allowedProtocol is the protocol arm of sanitizeUri's filter.
const allowedProtocol = new RegExp(`^(${safeProtocols.join('|')})$`, 'i')

const absolute = /^(https?:\/\/|mailto:)/i

// safeHref answers the address a rendered link may carry, or null. Only
// absolute web and mail links pass: a relative link would navigate the
// gateway's own pages, and every other scheme (javascript:, data:, file:)
// is refused. sanitizeUri also percent-encodes what a URL must not hold.
export function safeHref(url: string): string | null {
  const trimmed = url.trim()
  if (!absolute.test(trimmed)) {
    return null
  }
  const clean = sanitizeUri(trimmed, allowedProtocol)
  return clean === '' ? null : clean
}
