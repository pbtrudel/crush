import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import type { WireMessage } from '../api/wire'
import { ToolEnvironmentProvider } from '../tools/environment'
import MessageView from './MessageView'
import { resultIndex } from './parts'
import { streamedSourceInterval } from './useStreamedSource'

function message(parts: WireMessage['parts'], role = 'assistant', id = 'm1'): WireMessage {
  return { id, role, session_id: 's1', parts, created_at: 1, updated_at: 2 }
}

const text = (value: string) => ({ type: 'text', data: { text: value } })
const call = (id: string, name = 'bash') => ({
  type: 'tool_call',
  data: { id, name, input: '{"command":"ls"}', finished: true },
})
const finish = (reason: string, extra: Record<string, unknown> = {}) => ({
  type: 'finish',
  data: { reason, time: 9, ...extra },
})
const noResults = new Map()

function view(
  entry: WireMessage,
  turnActive = false,
  results: ReturnType<typeof resultIndex> = noResults,
) {
  return render(
    <ToolEnvironmentProvider>
      <MessageView message={entry} results={results} turnActive={turnActive} />
    </ToolEnvironmentProvider>,
  )
}

describe('MessageView', () => {
  it('keeps user text plain, markup included', () => {
    const { container } = view(message([text('**not bold** <b>x</b>')], 'user'))
    expect(screen.getByText('**not bold** <b>x</b>')).toBeInTheDocument()
    expect(container.querySelector('strong')).toBeNull()
  })

  it('renders assistant text as Markdown', () => {
    view(message([text('Use **bold**.')]))
    expect(screen.getByText('bold').tagName).toBe('STRONG')
  })

  it('draws the parts in the order they were written', () => {
    const { container } = view(message([text('First.'), call('tc-1'), text('Then.')]))
    const article = container.querySelector('article') as HTMLElement
    // Every segment is a wrapper div; a tool group's first child is its list.
    const order = [...article.children].map((child) =>
      child.firstElementChild?.tagName === 'UL' ? 'tools' : child.textContent,
    )
    expect(order.slice(0, 3)).toEqual(['First.', 'tools', 'Then.'])
  })

  it('groups consecutive calls in one list', () => {
    const { container } = view(message([call('tc-1'), call('tc-2', 'view')]))
    expect(container.querySelectorAll('ul')).toHaveLength(1)
    expect(container.querySelectorAll('[data-testid="tool-call"]')).toHaveLength(2)
  })

  it('collapses reasoning behind its duration', () => {
    const { container } = view(
      message([{ type: 'reasoning', data: { thinking: 'Look first.\nThen act.', started_at: 10, finished_at: 13 } }]),
    )
    const details = container.querySelector('details') as HTMLDetailsElement
    expect(details.open).toBe(false)
    expect(screen.getByText('Thought for 3s')).toBeInTheDocument()
    expect(details.querySelector('summary')).toHaveTextContent('Look first.')
  })

  it('says it is thinking while the reasoning streams', () => {
    view(message([{ type: 'reasoning', data: { thinking: 'Hmm' } }]), true)
    expect(screen.getByText('Thinking…')).toBeInTheDocument()
  })

  it('reads the duration once the reasoning is finished', () => {
    // The turn is still live, so the message streams, but this reasoning part
    // already carries its finish: it is not thinking any more.
    view(
      message([
        { type: 'reasoning', data: { thinking: 'Look first.', started_at: 10, finished_at: 13 } },
      ]),
      true,
    )
    expect(screen.getByText('Thought for 3s')).toBeInTheDocument()
    expect(screen.queryByText('Thinking…')).toBeNull()
  })

  it('renders nothing for a message of tool results', () => {
    const { container } = view(message([{ type: 'tool_result', data: { tool_call_id: 'tc-1', content: 'ok' } }], 'tool'))
    expect(container.querySelector('article')).toBeNull()
  })

  it('renders nothing it does not understand', () => {
    const { container } = view(message([{ type: 'unknown_part', data: {} }]))
    expect(container.querySelector('article')).toBeNull()
  })

  it('marks a call done from a result in another message', () => {
    const answered = message([{ type: 'tool_result', data: { tool_call_id: 'tc-1', content: 'ok' } }], 'tool', 't1')
    view(message([call('tc-1')]), false, resultIndex([answered]))
    expect(screen.getByText('Done')).toBeInTheDocument()
  })

  it('runs a call while its turn is live, and interrupts it when the turn is over', () => {
    const { unmount } = view(message([call('tc-1')]), true)
    expect(screen.getByText('Running')).toBeInTheDocument()
    unmount()
    view(message([call('tc-1')]), false)
    expect(screen.getByText('Interrupted')).toBeInTheDocument()
  })

  it('interrupts a call and says so when the turn was canceled', () => {
    view(message([call('tc-1'), finish('canceled')]), true)
    expect(screen.getByText('Interrupted')).toBeInTheDocument()
    expect(screen.getByRole('note')).toHaveTextContent('Canceled.')
  })

  it('shows why a turn stopped with an error', () => {
    view(message([text('Partial'), finish('error', { message: 'Rate limited' })]))
    expect(screen.getByRole('note')).toHaveTextContent('The turn stopped with an error. Rate limited')
  })

  it('reads a reason it does not know as no notice', () => {
    view(message([text('Hi'), finish('constructor')]))
    expect(screen.queryByRole('note')).toBeNull()
  })

  it('copies the text of a finished reply', async () => {
    const writeText = vi.fn(async () => undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    view(message([text('One.'), call('tc-1'), text('Two.'), finish('end_turn')]))
    screen.getByRole('button', { name: 'Copy message' }).click()
    await vi.waitFor(() => expect(writeText).toHaveBeenCalledWith('One.\n\nTwo.'))
  })

  it('offers no copy while the reply streams', () => {
    view(message([text('One.')]), true)
    expect(screen.queryByRole('button', { name: 'Copy message' })).toBeNull()
  })

  it('opens a tool row inside the message', async () => {
    view(message([call('tc-1', 'mystery')]))
    await userEvent.click(screen.getByRole('button', { name: /mystery/ }))
    expect(screen.getByRole('group', { name: 'Input' })).toBeInTheDocument()
  })

  // The M1 cases below moved here with the view: the message view is what
  // the M1 spec covered, and the coverage goes where the view went.

  it('hides a model-only text part', () => {
    const { container } = view(
      message([{ type: 'text', data: { text: 'environment snapshot', hidden: true } }]),
    )
    expect(container).toBeEmptyDOMElement()
  })

  it('keeps the visible parts of a message that also has hidden ones', () => {
    view(
      message(
        [{ type: 'text', data: { text: '<reminders>', hidden: true } }, text('Your turn.')],
        'user',
      ),
    )
    expect(screen.getByText('Your turn.')).toBeInTheDocument()
    expect(screen.queryByText('<reminders>')).toBeNull()
  })

  it('shows raw HTML in assistant text as text', () => {
    const { container } = view(message([text('<img src="x" onerror="alert(1)"> hello')]))
    expect(screen.getByText(/onerror/)).toBeInTheDocument()
    expect(container.querySelector('img')).toBeNull()
  })

  it('reads each call of a live turn by its own answer', () => {
    const answered = message(
      [{ type: 'tool_result', data: { tool_call_id: 'tc-1', content: 'ok' } }],
      'tool',
      't1',
    )
    view(message([call('tc-1'), call('tc-2', 'view')]), true, resultIndex([answered]))
    // The answer is beside the call it belongs to, not merely on screen.
    expect(screen.getByText('Done').closest('li')).toHaveTextContent('Bash')
    // The title is the view's now that "view" has one: Read.
    expect(screen.getByText('Running').closest('li')).toHaveTextContent('Read')
  })

  it('draws a streamed reply in passes and lands on the last delta', () => {
    vi.useFakeTimers()
    try {
      const { rerender } = view(message([text('One')]), true)
      const show = (value: string) =>
        rerender(
          <ToolEnvironmentProvider>
            <MessageView message={message([text(value)])} results={noResults} turnActive />
          </ToolEnvironmentProvider>,
        )
      // Two deltas inside one pass: the renderer is not asked to parse twice.
      show('One two')
      show('One two three')
      expect(screen.queryByText('One two three')).toBeNull()
      act(() => {
        vi.advanceTimersByTime(streamedSourceInterval)
      })
      expect(screen.getByText('One two three')).toBeInTheDocument()
    } finally {
      vi.useRealTimers()
    }
  })

  it('draws a shell command and a compaction note', () => {
    view(
      message([
        { type: 'shell_command', data: { command: 'ls', output: 'a.go', exit_code: 0 } },
        { type: 'compaction_stats', data: { saved_tokens: 43800 } },
      ]),
    )
    expect(screen.getByText(/\$ ls/)).toBeInTheDocument()
    expect(screen.getByText(/43.8K tokens/)).toBeInTheDocument()
  })
})
