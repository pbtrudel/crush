import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import Markdown, { maxMarkdownSource } from './Markdown'

describe('Markdown', () => {
  it('renders headings, emphasis, lists and inline code', () => {
    render(<Markdown source={'## Plan\n\n- **bold** and *soft*\n- `go test`\n\n1. first\n2. second'} />)
    expect(screen.getByRole('heading', { name: 'Plan' }).tagName).toBe('H3')
    expect(screen.getByText('bold').tagName).toBe('STRONG')
    expect(screen.getByText('soft').tagName).toBe('EM')
    expect(screen.getByText('go test').tagName).toBe('CODE')
    expect(screen.getAllByRole('list')).toHaveLength(2)
  })

  it('renders a GFM table with its alignment as a class', () => {
    const { container } = render(<Markdown source={'| a | b |\n| :-- | --: |\n| 1 | 2 |'} />)
    expect(screen.getByRole('table')).toBeInTheDocument()
    expect(container.querySelectorAll('th')).toHaveLength(2)
    expect(container.querySelector('td:last-child')?.className).toMatch(/alignRight/)
    expect(container.querySelector('[style]')).toBeNull()
  })

  it('renders a task list with disabled checkboxes', () => {
    render(<Markdown source={'- [x] done\n- [ ] open'} />)
    const boxes = screen.getAllByRole('checkbox')
    expect(boxes.map((box) => (box as HTMLInputElement).checked)).toEqual([true, false])
    expect(boxes.every((box) => (box as HTMLInputElement).disabled)).toBe(true)
  })

  it('opens a web link in a new tab without an opener', () => {
    render(<Markdown source={'See [Charm](https://charm.land).'} />)
    const link = screen.getByRole('link', { name: 'Charm' })
    expect(link).toHaveAttribute('href', 'https://charm.land/')
    expect(link).toHaveAttribute('target', '_blank')
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
  })

  // The address is an href, not an HTML string: a query keeps its amps.
  it('keeps a query string intact', () => {
    render(<Markdown source={'[q](https://x.com/?a=1&b=2)'} />)
    expect(screen.getByRole('link', { name: 'q' })).toHaveAttribute('href', 'https://x.com/?a=1&b=2')
  })

  it('resolves a reference link through its definition', () => {
    render(<Markdown source={'Read [the docs][d].\n\n[d]: https://charm.land/docs'} />)
    expect(screen.getByRole('link', { name: 'the docs' })).toHaveAttribute('href', 'https://charm.land/docs')
  })

  // CommonMark resolves character references in a destination, so this
  // address reaches safeHref as javascript: itself.
  it('keeps an entity-encoded script link inert', () => {
    const { container } = render(<Markdown source={'[a trap](java&#115;cript:alert(1))'} />)
    expect(container.querySelector('a')).toBeNull()
    expect(screen.getByText('a trap')).toBeInTheDocument()
  })

  it('keeps a protocol-relative link inert', () => {
    const { container } = render(<Markdown source={'[elsewhere](//evil.com/x)'} />)
    expect(container.querySelector('a')).toBeNull()
    expect(screen.getByText('elsewhere')).toBeInTheDocument()
  })

  it('keeps a reference definition pointing at a script link inert', () => {
    const { container } = render(<Markdown source={'[a trap][d].\n\n[d]: javascript:alert(1)'} />)
    expect(container.querySelector('a')).toBeNull()
    expect(screen.getByText('a trap')).toBeInTheDocument()
  })

  // The first definition wins, as in CommonMark.
  it('uses the first definition when one is repeated', () => {
    render(<Markdown source={'[docs][d].\n\n[d]: https://first.example\n[d]: https://second.example'} />)
    expect(screen.getByRole('link', { name: 'docs' })).toHaveAttribute('href', 'https://first.example/')
  })

  it('renders an autolink as a link', () => {
    render(<Markdown source={'See <https://charm.land>.'} />)
    expect(screen.getByRole('link')).toHaveAttribute('href', 'https://charm.land/')
  })

  it('keeps raw HTML inert: it is text, never an element', () => {
    const { container } = render(
      <Markdown source={'<img src="x" onerror="alert(1)">\n\nHi <script>alert(1)</script> there <b>bold</b>'} />,
    )
    expect(container.querySelector('img')).toBeNull()
    expect(container.querySelector('script')).toBeNull()
    expect(container.querySelector('b')).toBeNull()
    expect(container.textContent).toContain('<img src="x" onerror="alert(1)">')
    expect(container.textContent).toContain('<script>')
  })

  it('keeps a script link inert', () => {
    render(<Markdown source={'[a trap](javascript:alert(1))'} />)
    expect(screen.queryByRole('link')).toBeNull()
    expect(screen.getByText('a trap')).toBeInTheDocument()
  })

  // A relative address would navigate the gateway's own pages, so it is
  // text like a script link is: no anchor, no href.
  it('keeps a relative link inert', () => {
    const { container } = render(<Markdown source={'See [the route](/api/sessions) and [a file](parser.go).'} />)
    expect(screen.queryByRole('link')).toBeNull()
    expect(container.querySelector('a')).toBeNull()
    expect(screen.getByText('the route')).toBeInTheDocument()
    expect(screen.getByText('a file')).toBeInTheDocument()
  })

  it('never loads an image: it is a link to its address', () => {
    const { container } = render(<Markdown source={'![the logo](https://example.com/logo.png)'} />)
    expect(container.querySelector('img')).toBeNull()
    expect(screen.getByRole('link', { name: 'Image: the logo' })).toHaveAttribute('href', 'https://example.com/logo.png')
  })

  it('never loads a data image and never links one', () => {
    const { container } = render(<Markdown source={'![beacon](data:image/png;base64,AAAA)'} />)
    expect(container.querySelector('img')).toBeNull()
    expect(container.querySelector('a')).toBeNull()
    expect(container.textContent).toContain('Image: beacon')
  })

  it('renders a fenced block as code and keeps its text', () => {
    const { container } = render(<Markdown source={'```go\nfunc main() {}\n```'} />)
    expect(container.querySelector('pre')?.textContent).toContain('func main() {}')
  })

  it('reads $$ math and leaves a lone dollar alone', () => {
    const { container } = render(<Markdown source={'Cost $5, area $$\\pi r^2$$.\n\n$$\nx^2\n$$'} />)
    expect(container.textContent).toContain('Cost $5')
    expect(container.querySelectorAll('[data-math]')).toHaveLength(2)
  })

  it('renders an unterminated fence while it streams', () => {
    const { container } = render(<Markdown source={'Here:\n\n```ts\nconst a = 1'} />)
    expect(container.querySelector('pre')?.textContent).toContain('const a = 1')
  })

  // 2 KB of blockquote markers nests past the depth cap: it must render as
  // text, not walk a thousand stack frames.
  it('renders a deeply nested quote as text without throwing', () => {
    const source = '>'.repeat(2 * 1024)
    const { container } = render(<Markdown source={source} />)
    expect(container.textContent).toBe(source)
    expect(container.querySelector('blockquote')).toBeNull()
  })

  it('renders a source past the cap as plain text without parsing', () => {
    const source = 'x'.repeat(maxMarkdownSource + 1)
    const { container } = render(<Markdown source={source} />)
    expect(container.textContent).toBe(source)
    expect(container.querySelector('p')).toBeNull()
  })
})
