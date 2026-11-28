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
import todos from './TodosView.module.css?raw'

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
})

describe('the fetch views', () => {
  it('renders fetched Markdown through the safe renderer', async () => {
    const header = await open(
      call('fetch', { url: 'https://charm.land', format: 'markdown' }),
      result('# Charm\n\n<script>alert(1)</script>We build tools.'),
      /Fetch/,
    )
    expect(header).toHaveTextContent('https://charm.land')
    expect(screen.getByRole('heading', { name: 'Charm' })).toBeInTheDocument()
    expect(document.querySelector('script')).toBeNull()
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
      result('[a trap](javascript:alert(1)) and [the docs](https://charm.land/docs)'),
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
  it('lists every todo with its state and counts the done ones', async () => {
    const todos = [
      { content: 'Port the parser', status: 'completed', active_form: 'Porting the parser' },
      { content: 'Write the tests', status: 'in_progress', active_form: 'Writing the tests' },
      { content: 'Update the docs', status: 'pending', active_form: 'Updating the docs' },
    ]
    const header = await open(call('todos', { todos }), result('Todo list updated', { todos, completed: 1, total: 3 }), /Todos/)
    expect(header).toHaveTextContent('1/3 done')
    // The row itself is an li with a data-status too, so read inside it.
    const items = [...document.querySelectorAll<HTMLElement>('[data-testid="tool-call"] li[data-status]')]
    expect(items.map((item) => item.dataset.status)).toEqual(['completed', 'in_progress', 'pending'])
    expect(screen.getByText('Writing the tests')).toBeInTheDocument()
  })

  it('reads the list from the call while it runs', async () => {
    const todos = [{ content: 'Port the parser', status: 'in_progress', active_form: 'Porting the parser' }]
    const header = await open(call('todos', { todos }), undefined, /Todos/)
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
    const rows = () => document.querySelectorAll<HTMLElement>('[data-testid="tool-call"] li[data-status]')
    expect(rows()).toHaveLength(maxTodos)
    await userEvent.click(screen.getByRole('button', { name: `Show all ${many.length} todos` }))
    expect(rows()).toHaveLength(many.length)
    view.rerender(tree(null, undefined))
    view.rerender(tree(call('todos', { todos: many }), answer))
    expect(rows()).toHaveLength(many.length)
  })

  it('bounds one row of text the model wrote', async () => {
    const long = 'w'.repeat(5000)
    const todos = [{ content: long, status: 'pending', active_form: '' }]
    await open(call('todos', { todos }), result('Todo list updated', { todos, completed: 0, total: 1 }), /Todos/)
    const row = document.querySelector<HTMLElement>('[data-testid="tool-call"] li[data-status]')
    expect(row?.textContent?.length).toBeLessThan(1000)
    expect(row).toHaveTextContent('…')
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
    const value = rule(todos, 'color', 'completed')
    expect(value).toBe('var(--dsw-alias-label-secondary)')
    const surface = rgbOf('var(--dsw-alias-bg-layer-1)', sheet)
    expect(contrast(rgbOf(value ?? '', sheet), surface)).toBeGreaterThanOrEqual(4.5)
  })

  it('names its colours in classes, never in a style attribute', () => {
    expect(todos).not.toMatch(/\bstyle\s*=/)
  })
})
