import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import type { ToolCallPart, ToolResultPart } from '../../chat/parts'
import { contrast, rgbOf, vars } from '../../theme/contrast'
import design from '../../theme/design-platform.css?raw'
import { ToolEnvironmentProvider } from '../environment'
import ToolCall from '../ToolCall'
import { toolView } from '../registry'
import { genericView } from './GenericView'
import { mcpView, splitMcpName } from './McpView'
import { maxTodos } from './TodosView'
import todosCss from './TodosView.module.css?raw'

function call(name: string, input: Record<string, unknown>): ToolCallPart {
  return { id: 'tc-1', name, input: JSON.stringify(input), finished: true }
}

function result(
  content: string,
  metadata: Record<string, unknown> | null = null,
  extra: Partial<ToolResultPart> = {},
): ToolResultPart {
  return {
    toolCallId: 'tc-1',
    name: '',
    content,
    mimeType: '',
    metadata: metadata ? JSON.stringify(metadata) : '',
    isError: false,
    ...extra,
  }
}

// tree mounts one row under the environment. A null entry unmounts the row
// and leaves the environment's sets in place, which is how a virtualized
// log takes a row away and brings it back.
function tree(entry: ToolCallPart | null, answer: ToolResultPart | undefined, servers: readonly string[] = []) {
  return (
    <ToolEnvironmentProvider mcpServers={servers}>
      <ul>
        {entry === null ? null : (
          <ToolCall call={entry} result={answer} status={answer === undefined ? 'running' : 'done'} messageId="m1" depth={0} />
        )}
      </ul>
    </ToolEnvironmentProvider>
  )
}

async function open(
  entry: ToolCallPart,
  answer: ToolResultPart | undefined,
  title: RegExp,
  servers: string[] = [],
) {
  render(tree(entry, answer, servers))
  const header = screen.getByRole('button', { name: title })
  await userEvent.click(header)
  return header
}

describe('the search views', () => {
  it('summarises a grep by its pattern, place and count', async () => {
    const header = await open(
      call('grep', { pattern: 'func Parse', path: '/tmp/project' }),
      result('Found 1 matches\n/tmp/project/parser.go:\n  Line 3: func Parse() {}', {
        number_of_matches: 1,
        truncated: false,
      }),
      /Grep/,
    )
    expect(header).toHaveTextContent('func Parse in /tmp/project · 1 match')
    expect(screen.getByText(/Line 3: func Parse/)).toBeInTheDocument()
  })

  it('counts glob files and says when the list was cut', async () => {
    const header = await open(
      call('glob', { pattern: '**/*.go' }),
      result('/tmp/project/parser.go\n/tmp/project/lexer.go', { number_of_files: 2, truncated: true }),
      /Glob/,
    )
    expect(header).toHaveTextContent('**/*.go · 2 files')
    expect(screen.getByText('The list was cut short.')).toBeInTheDocument()
  })

  it('lists a directory by its place and file count', async () => {
    const header = await open(
      call('ls', { path: '/tmp/project' }),
      result('parser.go\nlexer.go\nreadme.md', { number_of_files: 3, truncated: false }),
      /List/,
    )
    expect(header).toHaveTextContent('/tmp/project · 3 files')
  })

  it('clips a long result until the person asks for the rest', async () => {
    const found = Array.from({ length: 60 }, (_, index) => `/tmp/project/file${index}.go: Line 1: hit`)
    const view = render(tree(call('grep', { pattern: 'hit' }), result(found.join('\n'), { number_of_matches: 60 })))
    await userEvent.click(screen.getByRole('button', { name: /Grep/ }))
    expect(screen.queryByText(/file59\.go/)).toBeNull()
    await userEvent.click(screen.getByRole('button', { name: 'Show all 60 lines' }))
    expect(screen.getByText(/file59\.go/)).toBeInTheDocument()
    // The reveal lives above the row, so a remount of the virtualized log
    // finds the output still open.
    view.rerender(tree(null, undefined))
    view.rerender(tree(call('grep', { pattern: 'hit' }), result(found.join('\n'), { number_of_matches: 60 })))
    expect(screen.getByText(/file59\.go/)).toBeInTheDocument()
  })

  // A cut search stops at its cap, so its count is a floor, and its body
  // already ends with the tool's own truncation note: the view adds none.
  it('reads a cut count as a floor and leaves the note to the tool', async () => {
    const body = [
      'Found 240 matches, showing the first 100',
      '',
      '/tmp/project/parser.go:',
      '  Line 3: hit',
      '',
      '(Results are truncated. Consider using a more specific path or pattern.)',
    ].join('\n')
    const header = await open(
      call('grep', { pattern: 'hit' }),
      result(body, { number_of_matches: 100, truncated: true }),
      /Grep/,
    )
    expect(header).toHaveTextContent('hit · 100+ matches')
    expect(screen.queryByText('The list was cut short.')).toBeNull()
    expect(screen.getByText(/Results are truncated/)).toBeInTheDocument()
  })

  // A body with no note of its own still gets the view's.
  it('says a cut search stopped when the body does not', async () => {
    await open(
      call('grep', { pattern: 'hit' }),
      result('Found 100 matches\n/tmp/project/parser.go:\n  Line 3: hit', { number_of_matches: 100, truncated: true }),
      /Grep/,
    )
    expect(screen.getByText('The list was cut short.')).toBeInTheDocument()
  })
})

describe('the fetch views', () => {
  // fenced is what the fetch tool writes for format markdown: the page's
  // Markdown inside a fence (internal/agent/tools/fetch.go).
  function fenced(body: string): string {
    return `\`\`\`\n${body}\n\`\`\``
  }

  it('renders fetched Markdown through the safe renderer', async () => {
    const header = await open(
      call('fetch', { url: 'https://charm.land', format: 'markdown' }),
      result(fenced('# Charm\n\n<script>alert(1)</script>We build tools.')),
      /Fetch/,
    )
    expect(header).toHaveTextContent('https://charm.land')
    expect(screen.getByRole('heading', { name: 'Charm' })).toBeInTheDocument()
    expect(document.querySelector('script')).toBeNull()
    expect(document.body.textContent).not.toContain('```')
  })

  // A page cut at the size limit loses the fence's closing line.
  it('renders an answer whose closing fence was cut off', async () => {
    await open(
      call('fetch', { url: 'https://charm.land', format: 'markdown' }),
      result('```\n# Charm\n\nWe build tools.'),
      /Fetch/,
    )
    expect(screen.getByRole('heading', { name: 'Charm' })).toBeInTheDocument()
    expect(document.body.textContent).not.toContain('```')
  })

  // Only the tool's own fence goes: a fenced block the page wrote stays.
  it('keeps a fence the page wrote itself', async () => {
    await open(
      call('fetch', { url: 'https://charm.land', format: 'markdown' }),
      result(fenced('# Charm\n\n```go\nfunc main() {}\n```')),
      /Fetch/,
    )
    expect(screen.getByRole('heading', { name: 'Charm' })).toBeInTheDocument()
    expect(document.body.textContent).toContain('func main() {}')
  })

  it('shows fetched HTML as text', async () => {
    await open(call('fetch', { url: 'https://charm.land', format: 'html' }), result('<h1>Charm</h1>'), /Fetch/)
    expect(screen.getByText('<h1>Charm</h1>')).toBeInTheDocument()
  })

  // A page is untrusted text: a link it carries renders only when it is an
  // absolute web or mail address.
  it('keeps a link a page carries inert', async () => {
    await open(
      call('fetch', { url: 'https://charm.land', format: 'markdown' }),
      result(fenced('[a trap](javascript:alert(1)) and [the docs](https://charm.land/docs)')),
      /Fetch/,
    )
    const links = [...document.querySelectorAll('a')]
    expect(links.map((link) => link.getAttribute('href'))).toEqual(['https://charm.land/docs'])
  })

  it('titles a web fetch by its URL and a web search by its query', async () => {
    const header = await open(
      call('web_fetch', { url: 'https://charm.land' }),
      result('Charm builds tools.'),
      /Fetch/,
    )
    expect(header).toHaveTextContent('https://charm.land')
    render(tree(call('web_search', { query: 'charm cli' }), result('1. Charm — a CLI')))
    expect(screen.getByRole('button', { name: /Search the web/ })).toHaveTextContent('charm cli')
  })
})

describe('the todos view', () => {
  // rows is the todo list drawn in the open row.
  function rows(): HTMLElement[] {
    return [...document.querySelectorAll<HTMLElement>('[data-testid="tool-call"] li[data-status]')]
  }

  // rowText is a row's own text, past the glyph the row starts with.
  function rowText(row: HTMLElement): string {
    return row.querySelectorAll('span')[1]?.textContent ?? ''
  }

  it('lists every todo with its state and counts the done ones', async () => {
    const todos = [
      { content: 'Port the parser', status: 'completed', active_form: 'Porting the parser' },
      { content: 'Write the tests', status: 'in_progress', active_form: 'Writing the tests' },
      { content: 'Update the docs', status: 'pending', active_form: 'Updating the docs' },
    ]
    const header = await open(call('todos', { todos }), result('Todo list updated', { todos, completed: 1, total: 3 }), /Todos/)
    expect(header).toHaveTextContent('1/3 done')
    expect(rows().map((item) => item.dataset.status)).toEqual(['completed', 'in_progress', 'pending'])
    expect(screen.getByText('Writing the tests')).toBeInTheDocument()
  })

  it('reads the list from the call while it runs', async () => {
    const todos = [{ content: 'Port the parser', status: 'in_progress', active_form: 'Porting the parser' }]
    const header = await open(call('todos', { todos }), undefined, /Todos/)
    expect(header).toHaveTextContent('0/1 done')
    expect(screen.getByText('Porting the parser')).toBeInTheDocument()
  })

  // Some models send the array as a JSON string; todos.go decodes that shape,
  // so a running row must show the list too.
  it('reads a list the model sent as a JSON string', async () => {
    const todos = [{ content: 'Port the parser', status: 'in_progress', active_form: 'Porting the parser' }]
    const header = await open(call('todos', { todos: JSON.stringify(todos) }), undefined, /Todos/)
    expect(header).toHaveTextContent('0/1 done')
    expect(screen.getByText('Porting the parser')).toBeInTheDocument()
  })

  it('clips a long list and shows it whole when asked', async () => {
    const many = Array.from({ length: maxTodos + 5 }, (_, index) => ({
      content: `Todo ${index + 1}`,
      status: 'pending',
      active_form: '',
    }))
    const answer = result('Todo list updated', { todos: many, completed: 0, total: many.length })
    const view = render(tree(call('todos', { todos: many }), answer))
    await userEvent.click(screen.getByRole('button', { name: /Todos/ }))
    expect(rows()).toHaveLength(maxTodos)
    await userEvent.click(screen.getByRole('button', { name: `Show all ${many.length} todos` }))
    expect(rows()).toHaveLength(many.length)
    view.rerender(tree(null, undefined))
    view.rerender(tree(call('todos', { todos: many }), answer))
    expect(rows()).toHaveLength(many.length)
  })

  // A short list can still hold one enormous todo, and a cut row needs the
  // same reveal a cut list gets.
  it('reveals one long row on its own', async () => {
    const long = 'w'.repeat(5000)
    const todos = [
      { content: 'Port the parser', status: 'completed', active_form: '' },
      { content: long, status: 'pending', active_form: '' },
      { content: 'Update the docs', status: 'pending', active_form: '' },
    ]
    await open(call('todos', { todos }), result('Todo list updated', { todos, completed: 1, total: 3 }), /Todos/)
    expect(rowText(rows()[1]).length).toBeLessThan(1000)
    expect(rowText(rows()[1])).toContain('…')
    await userEvent.click(screen.getByRole('button', { name: 'Show all 3 todos' }))
    expect(rowText(rows()[1])).toBe(long)
  })

  // A rejected list was never saved, so the row shows what the tool refused
  // instead of a list that reads as applied.
  it('shows a rejected list as the error the tool reported', async () => {
    const todos = [{ content: 'Write the tests', status: 'done', active_form: '' }]
    const error = 'Invalid status "done" for todo "Write the tests" (must be pending, in_progress, or completed)'
    const header = await open(call('todos', { todos }), result(error, null, { isError: true }), /Todos/)
    expect(header).not.toHaveTextContent(/\d+\/\d+ done/)
    expect(screen.getByRole('group', { name: 'Error' })).toHaveTextContent(error)
    expect(rows()).toHaveLength(0)
  })

  // A status is model text: a name the object happens to carry, such as
  // "constructor", must fall back to the pending rule.
  it('falls back to the pending style for a status it does not know', async () => {
    const todos = [{ content: 'Port the parser', status: 'constructor', active_form: '' }]
    await open(call('todos', { todos }), result('Todo list updated', { todos, completed: 0, total: 1 }), /Todos/)
    expect(rows()[0]).toHaveAttribute('data-status', 'constructor')
    expect(rows()[0].className).toContain('pending')
  })
})

describe('the MCP view', () => {
  it('splits a server whose name has an underscore by the known servers', () => {
    expect(splitMcpName('mcp_my_server_search', ['my_server', 'my'])).toEqual({ server: 'my_server', tool: 'search' })
    expect(splitMcpName('mcp_github_search_issues', [])).toEqual({ server: 'github', tool: 'search_issues' })
  })

  it('titles the call by server and tool and shows its output', async () => {
    const header = await open(
      call('mcp_github_search_issues', { query: 'parser bug' }),
      result('#12 Parser drops comments'),
      /github/,
      ['github'],
    )
    expect(header).toHaveTextContent('github › search_issues')
    expect(header).toHaveTextContent('parser bug')
    expect(screen.getByText('#12 Parser drops comments')).toBeInTheDocument()
  })

  it('names an image result instead of loading it', async () => {
    await open(call('mcp_shots_capture', {}), result('', null, { mimeType: 'image/png' }), /shots/)
    expect(screen.getByText('The server answered with an image (image/png).')).toBeInTheDocument()
    expect(document.querySelector('img')).toBeNull()
  })

  // The text beside an image is what the server said about it, so it is
  // shown under the note rather than dropped.
  it('shows the text an image answer carries', async () => {
    await open(
      call('mcp_shots_capture', {}),
      result('Captured /tmp/shot.png at 1440x900', null, { mimeType: 'image/png' }),
      /shots/,
    )
    expect(screen.getByText('The server answered with an image (image/png).')).toBeInTheDocument()
    expect(screen.getByText('Captured /tmp/shot.png at 1440x900')).toBeInTheDocument()
    expect(document.querySelector('img')).toBeNull()
  })

  it('clips a long output and keeps the reveal across a remount', async () => {
    const lines = Array.from({ length: 50 }, (_, index) => `line ${index + 1}`).join('\n')
    const entry = call('mcp_github_search_issues', { query: 'parser bug' })
    const answer = result(lines)
    const view = render(tree(entry, answer, ['github']))
    await userEvent.click(screen.getByRole('button', { name: /github/ }))
    expect(screen.queryByText(/line 50/)).toBeNull()
    await userEvent.click(screen.getByRole('button', { name: 'Show all 50 lines' }))
    expect(screen.getByText(/line 50/)).toBeInTheDocument()
    view.rerender(tree(null, undefined))
    view.rerender(tree(entry, answer, ['github']))
    expect(screen.getByText(/line 50/)).toBeInTheDocument()
  })
})

describe('the registry', () => {
  // mcp_servers is a built-in tool whose name wears the MCP prefix, so it
  // needs its own entry ahead of the prefix rule.
  it('keeps the built-in mcp_servers tool out of the MCP view', async () => {
    expect(toolView('mcp_servers')).toBe(genericView)
    expect(toolView('mcp_github_search_issues')).toBe(mcpView)
    expect(toolView('grep')).not.toBe(genericView)
    // Taken for a server call, the title would read "servers" and the tool
    // name would never reach the header.
    const header = await open(call('mcp_servers', {}), result('docker: connected'), /mcp_servers/)
    expect(header).toHaveTextContent('mcp_servers')
    expect(screen.getByText('docker: connected')).toBeInTheDocument()
  })
})

// rule reads one property of one class rule from a module stylesheet, the
// way diff.test.ts does.
function rule(sheet: string, property: 'color', name: string): string | null {
  let found: string | null = null
  const source = sheet.replace(/\/\*[\s\S]*?\*\//g, '')
  for (const block of source.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    const selectors = block[1].split(',').map((selector) => selector.trim())
    if (!selectors.includes(`.${name}`)) {
      continue
    }
    const value = new RegExp(`${property}:\\s*([^;]+);`).exec(block[2])
    if (value) {
      found = value[1].trim()
    }
  }
  return found
}

describe('the todos colours', () => {
  const sheet = vars(design, 'light')

  // A finished todo is struck through and drawn in the secondary label
  // colour. The card is white in the light theme, so the colour has to
  // reach AA there.
  it('keeps a completed row readable on the card', () => {
    const value = rule(todosCss, 'color', 'completed')
    expect(value).toBe('var(--dsw-alias-label-secondary)')
    const surface = rgbOf('var(--dsw-alias-bg-layer-1)', sheet)
    expect(contrast(rgbOf(value ?? '', sheet), surface)).toBeGreaterThanOrEqual(4.5)
  })

  it('names its colours in classes, never in a style attribute', () => {
    expect(todosCss).not.toMatch(/\bstyle\s*=/)
  })
})
