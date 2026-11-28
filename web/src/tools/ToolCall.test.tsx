import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import type { ToolCallPart, ToolResultPart } from '../chat/parts'
import { ToolEnvironmentProvider } from './environment'
import OutputBlock, { maxOutputChars } from './OutputBlock'
import ToolCall from './ToolCall'

const call: ToolCallPart = { id: 'tc-1', name: 'mystery_tool', input: '{"target":"parser.go"}', finished: true }

function result(overrides: Partial<ToolResultPart> = {}): ToolResultPart {
  return { toolCallId: 'tc-1', name: 'mystery_tool', content: 'it worked', mimeType: '', metadata: '', isError: false, ...overrides }
}

function tree(props: Partial<Parameters<typeof ToolCall>[0]> = {}) {
  return (
    <ToolEnvironmentProvider>
      <ul>
        <ToolCall call={call} result={result()} status="done" messageId="m1" depth={0} {...props} />
      </ul>
    </ToolEnvironmentProvider>
  )
}

function row(props: Partial<Parameters<typeof ToolCall>[0]> = {}) {
  return render(tree(props))
}

function emptyTree() {
  return (
    <ToolEnvironmentProvider>
      <ul />
    </ToolEnvironmentProvider>
  )
}

describe('ToolCall', () => {
  it('shows the title, the summary and the status, closed', () => {
    row()
    const header = screen.getByRole('button', { name: /mystery_tool/ })
    expect(header).toHaveAttribute('aria-expanded', 'false')
    expect(header).toHaveTextContent('parser.go')
    expect(header).toHaveTextContent('Done')
    expect(screen.queryByText('it worked')).toBeNull()
  })

  it('opens to the input and the output', async () => {
    row()
    await userEvent.click(screen.getByRole('button', { name: /mystery_tool/ }))
    expect(screen.getByRole('button', { name: /mystery_tool/ })).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByText(/"target": "parser.go"/)).toBeInTheDocument()
    expect(screen.getByText('it worked')).toBeInTheDocument()
  })

  it('labels a failed call and its error output', async () => {
    row({ status: 'error', result: result({ isError: true, content: 'no such file' }) })
    expect(screen.getByText('Failed')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: /mystery_tool/ }))
    expect(screen.getByRole('group', { name: 'Error' })).toHaveTextContent('no such file')
  })

  it('names every status', () => {
    const { rerender } = row({ status: 'running', result: undefined })
    expect(screen.getByText('Running')).toBeInTheDocument()
    rerender(
      <ToolEnvironmentProvider>
        <ul>
          <ToolCall call={call} result={undefined} status="interrupted" messageId="m1" depth={0} />
        </ul>
      </ToolEnvironmentProvider>,
    )
    expect(screen.getByText('Interrupted')).toBeInTheDocument()
  })

  it('carries the tool and status for the browser specs', () => {
    const { container } = row()
    const item = container.querySelector('[data-testid="tool-call"]')
    expect(item).toHaveAttribute('data-tool', 'mystery_tool')
    expect(item).toHaveAttribute('data-status', 'done')
  })

  it('points aria-controls at the body only while open', async () => {
    row()
    const header = screen.getByRole('button', { name: /mystery_tool/ })
    expect(header).not.toHaveAttribute('aria-controls')
    await userEvent.click(header)
    const bodyId = header.getAttribute('aria-controls')
    expect(bodyId).not.toBeNull()
    expect(document.getElementById(bodyId ?? '')).not.toBeNull()
    await userEvent.click(header)
    expect(header).not.toHaveAttribute('aria-controls')
  })

  it('opens from the keyboard', async () => {
    row()
    const header = screen.getByRole('button', { name: /mystery_tool/ })
    header.focus()
    await userEvent.keyboard('{Enter}')
    expect(header).toHaveAttribute('aria-expanded', 'true')
    await userEvent.keyboard(' ')
    expect(header).toHaveAttribute('aria-expanded', 'false')
  })

  it('keeps the row open across a remount', async () => {
    const { rerender } = row()
    await userEvent.click(screen.getByRole('button', { name: /mystery_tool/ }))
    rerender(emptyTree())
    rerender(tree())
    expect(screen.getByRole('button', { name: /mystery_tool/ })).toHaveAttribute('aria-expanded', 'true')
  })

  it('clips the input of a huge string argument', async () => {
    const body = 'y'.repeat(100 * 1024)
    const huge: ToolCallPart = { id: 'tc-2', name: 'mystery_tool', input: JSON.stringify({ body }), finished: true }
    row({ call: huge })
    await userEvent.click(screen.getByRole('button', { name: /mystery_tool/ }))
    const input = screen.getByRole('group', { name: 'Input' })
    expect(input.querySelector('pre')?.textContent).toHaveLength(maxOutputChars)
    expect(within(input).getByRole('button', { name: /^Show all \(\d+ K characters\)$/ })).toBeInTheDocument()
  })

  it('clips a lined input to 40 lines, not to its first 20000 characters', async () => {
    const entries = Object.fromEntries(Array.from({ length: 100 }, (_, index) => [`k${index}`, 'z'.repeat(300)]))
    const lined: ToolCallPart = { id: 'tc-3', name: 'mystery_tool', input: JSON.stringify(entries), finished: true }
    row({ call: lined })
    await userEvent.click(screen.getByRole('button', { name: /mystery_tool/ }))
    const input = screen.getByRole('group', { name: 'Input' })
    expect(input.querySelector('pre')?.textContent?.split('\n')).toHaveLength(40)
    expect(within(input).getByRole('button', { name: 'Show all 102 lines' })).toBeInTheDocument()
  })

  it('keeps a revealed output across a remount', async () => {
    const text = Array.from({ length: 50 }, (_, index) => `line ${index + 1}`).join('\n')
    const props = { result: result({ content: text }) }
    const { rerender } = row(props)
    await userEvent.click(screen.getByRole('button', { name: /mystery_tool/ }))
    await userEvent.click(screen.getByRole('button', { name: 'Show all 50 lines' }))
    expect(screen.getByText(/line 50/)).toBeInTheDocument()
    rerender(emptyTree())
    rerender(tree(props))
    expect(screen.getByText(/line 50/)).toBeInTheDocument()
  })
})

describe('OutputBlock', () => {
  it('clips a long output until asked', async () => {
    const text = Array.from({ length: 50 }, (_, index) => `line ${index + 1}`).join('\n')
    render(<OutputBlock text={text} maxLines={10} />)
    expect(screen.queryByText(/line 11/)).toBeNull()
    await userEvent.click(screen.getByRole('button', { name: 'Show all 50 lines' }))
    expect(screen.getByText(/line 50/)).toBeInTheDocument()
  })

  it('clips a single huge line by characters', () => {
    const { container } = render(<OutputBlock text={'x'.repeat(100 * 1024)} />)
    expect(container.querySelector('pre')?.textContent).toHaveLength(maxOutputChars)
    expect(screen.getByRole('button', { name: 'Show all (103 K characters)' })).toBeInTheDocument()
  })

  it('clips by lines before characters', () => {
    const text = Array.from({ length: 500 }, () => 'x'.repeat(100)).join('\n')
    const { container } = render(<OutputBlock text={text} />)
    expect(container.querySelector('pre')?.textContent?.split('\n')).toHaveLength(40)
    expect(screen.getByRole('button', { name: 'Show all 500 lines' })).toBeInTheDocument()
  })

  it('never ends the cut on half a surrogate pair', () => {
    const text = `${'a'.repeat(maxOutputChars - 1)}😀b`
    const { container } = render(<OutputBlock text={text} />)
    expect(container.querySelector('pre')?.textContent).toBe('a'.repeat(maxOutputChars - 1))
  })

  it('does not count a trailing newline as a line', () => {
    const text = `${Array.from({ length: 40 }, (_, index) => `line ${index + 1}`).join('\n')}\n`
    render(<OutputBlock text={text} />)
    expect(screen.queryByRole('button')).toBeNull()
    expect(screen.getByText(/line 40/)).toBeInTheDocument()
  })
})
