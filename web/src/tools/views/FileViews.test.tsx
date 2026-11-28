import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import type { ToolCallPart, ToolResultPart } from '../../chat/parts'
import { maxDiffInputChars } from '../diff'
import { ToolEnvironmentProvider } from '../environment'
import ToolCall from '../ToolCall'

function call(name: string, input: Record<string, unknown>): ToolCallPart {
  return { id: 'tc-1', name, input: JSON.stringify(input), finished: true }
}

function result(metadata: Record<string, unknown> | null, content = ''): ToolResultPart {
  return {
    toolCallId: 'tc-1',
    name: '',
    content,
    mimeType: '',
    metadata: metadata ? JSON.stringify(metadata) : '',
    isError: false,
  }
}

async function open(entry: ToolCallPart, answer: ToolResultPart | undefined, title: RegExp) {
  const view = render(
    <ToolEnvironmentProvider>
      <ul>
        <ToolCall call={entry} result={answer} status={answer ? 'done' : 'running'} messageId="m1" depth={0} />
      </ul>
    </ToolEnvironmentProvider>,
  )
  await userEvent.click(screen.getByRole('button', { name: title }))
  return view
}

describe('the file views', () => {
  it('reads a file slice with its own line numbers', async () => {
    const { container } = await open(
      call('view', { file_path: '/tmp/project/parser.go', offset: 40 }),
      result({ file_path: '/tmp/project/parser.go', content: 'package parser\nfunc Parse() {}' }),
      /Read/,
    )
    expect(screen.getByRole('button', { name: /Read/ })).toHaveTextContent('/tmp/project/parser.go')
    const gutters = [...container.querySelectorAll('[aria-hidden="true"]')].map((node) => node.textContent)
    expect(gutters).toEqual(['41', '42'])
    await waitFor(() => expect(container.querySelector('[class*="tok-"]')).not.toBeNull(), { timeout: 10_000 })
  })

  it('shows an edit as a diff with its counts', async () => {
    await open(
      call('edit', { file_path: '/tmp/project/parser.go', old_string: 'a', new_string: 'b' }),
      result({ additions: 1, removals: 1, old_content: 'x\nold\ny\n', new_content: 'x\nnew\ny\n' }),
      /Edit/,
    )
    expect(screen.getByRole('button', { name: /Edit/ })).toHaveTextContent('+1 −1')
    expect(screen.getByText('old').closest('[data-kind]')).toHaveAttribute('data-kind', 'del')
    expect(screen.getByText('new').closest('[data-kind]')).toHaveAttribute('data-kind', 'add')
  })

  it('diffs the call itself while the edit runs', async () => {
    await open(call('edit', { file_path: '/tmp/a.go', old_string: 'before', new_string: 'after' }), undefined, /Edit/)
    expect(screen.getByText('before').closest('[data-kind]')).toHaveAttribute('data-kind', 'del')
    expect(screen.getByText('after').closest('[data-kind]')).toHaveAttribute('data-kind', 'add')
  })

  // The arguments arrive one token at a time; diffing each half-written
  // pair would cost a pass per delta and say nothing.
  it('waits for the arguments before diffing them', async () => {
    await open(
      {
        ...call('edit', { file_path: '/tmp/a.go', old_string: 'before', new_string: 'after' }),
        finished: false,
      },
      undefined,
      /Edit/,
    )
    expect(screen.getByText('Editing…')).toBeInTheDocument()
    expect(screen.queryByText('before')).toBeNull()
  })

  it('shows a write from the diff its metadata carries', async () => {
    await open(
      call('write', { file_path: '/tmp/notes.md', content: '# Notes\nnew line\n' }),
      result({
        diff: '--- a/notes.md\n+++ b/notes.md\n@@ -1,2 +1,2 @@\n # Notes\n-old line\n+new line\n',
        additions: 1,
        removals: 1,
      }),
      /Write/,
    )
    expect(screen.getByText('new line').closest('[data-kind]')).toHaveAttribute('data-kind', 'add')
    expect(screen.getByRole('button', { name: /Write/ })).toHaveTextContent('+1 −1')
  })

  it('shows the error of an edit that failed', async () => {
    await open(
      call('edit', { file_path: '/tmp/a.go', old_string: 'x', new_string: 'y' }),
      { ...result(null, 'old_string not found in file'), isError: true },
      /Edit/,
    )
    expect(screen.getByRole('group', { name: 'Error' })).toHaveTextContent('old_string not found in file')
  })

  it('summarises a read of several files by its count', () => {
    render(
      <ToolEnvironmentProvider>
        <ul>
          <ToolCall
            call={call('read', { file_paths: ['/tmp/a.go', '/tmp/b.go'] })}
            result={undefined}
            status="running"
            messageId="m1"
            depth={0}
          />
        </ul>
      </ToolEnvironmentProvider>,
    )
    expect(screen.getByRole('button', { name: /Read/ })).toHaveTextContent('2 files')
  })

  // A long diff is clipped the way an output block is: forty rows, then
  // 20000 characters. The reveal lives in the environment, so the row the
  // virtualized log unmounts and mounts again is still whole.
  it('clips a long diff and keeps the reveal across a remount', async () => {
    const before = Array.from({ length: 30 }, (_, index) => `old ${index + 1}`).join('\n')
    const after = Array.from({ length: 30 }, (_, index) => `new ${index + 1}`).join('\n')
    const entry = call('edit', { file_path: '/tmp/a.go', old_string: 'x', new_string: 'y' })
    const answer = result({ additions: 30, removals: 30, old_content: before, new_content: after })
    const view = await open(entry, answer, /Edit/)
    expect(screen.queryByText('new 30')).toBeNull()
    await userEvent.click(screen.getByRole('button', { name: 'Show all 61 lines' }))
    expect(screen.getByText('new 30')).toBeInTheDocument()
    view.rerender(
      <ToolEnvironmentProvider>
        <ul />
      </ToolEnvironmentProvider>,
    )
    view.rerender(
      <ToolEnvironmentProvider>
        <ul>
          <ToolCall call={entry} result={answer} status="done" messageId="m1" depth={0} />
        </ul>
      </ToolEnvironmentProvider>,
    )
    expect(screen.getByText('new 30')).toBeInTheDocument()
  })

  // The file contents are capped before jsdiff runs; past the cap the view
  // says so instead of looking like a diff of the whole file.
  it('says when a diff was too large to show whole', async () => {
    const before = `a${'x'.repeat(maxDiffInputChars)}`
    const after = `b${'x'.repeat(maxDiffInputChars)}`
    await open(
      call('edit', { file_path: '/tmp/big.go', old_string: 'a', new_string: 'b' }),
      result({ additions: 1, removals: 1, old_content: before, new_content: after }),
      /Edit/,
    )
    expect(screen.getByText(/Diff too large/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Show all \(/ })).toBeInTheDocument()
  })

  // Two versions that share no line are too far apart to diff; the view
  // says so rather than looking like a file with no change.
  it('says when an edit changes too many lines to diff', async () => {
    const before = 'alpha\n'.repeat(600)
    const after = 'beta\n'.repeat(600)
    await open(
      call('edit', { file_path: '/tmp/a.go', old_string: 'alpha', new_string: 'beta' }),
      result({ additions: 600, removals: 600, old_content: before, new_content: after }),
      /Edit/,
    )
    expect(screen.getByText(/Diff too large/)).toBeInTheDocument()
    expect(screen.queryByText('No changes')).toBeNull()
  })

  it('shows a multiedit as one diff of the file it rewrote', async () => {
    await open(
      call('multiedit', {
        file_path: '/tmp/a.go',
        edits: [
          { old_string: 'one', new_string: 'two' },
          { old_string: 'three', new_string: 'four' },
        ],
      }),
      result({ additions: 2, removals: 2, old_content: 'one\nthree\n', new_content: 'two\nfour\n' }),
      /Edit/,
    )
    expect(screen.getByText('one').closest('[data-kind]')).toHaveAttribute('data-kind', 'del')
    expect(screen.getByText('four').closest('[data-kind]')).toHaveAttribute('data-kind', 'add')
  })

  it('diffs the edits of a multiedit still running', async () => {
    await open(
      call('multiedit', {
        file_path: '/tmp/a.go',
        edits: [
          { old_string: 'one', new_string: 'two' },
          { old_string: 'three', new_string: 'four' },
        ],
      }),
      undefined,
      /Edit/,
    )
    expect(screen.getByText('one').closest('[data-kind]')).toHaveAttribute('data-kind', 'del')
    expect(screen.getByText('four').closest('[data-kind]')).toHaveAttribute('data-kind', 'add')
  })

  // Some models send the edits array as a JSON string; the tool decodes
  // that too, so the view reads it the same way.
  it('reads a multiedit whose edits arrived as a string', async () => {
    await open(
      call('multiedit', {
        file_path: '/tmp/a.go',
        edits: JSON.stringify([{ old_string: 'one', new_string: 'two' }]),
      }),
      undefined,
      /Edit/,
    )
    expect(screen.getByText('one').closest('[data-kind]')).toHaveAttribute('data-kind', 'del')
    expect(screen.getByText('two').closest('[data-kind]')).toHaveAttribute('data-kind', 'add')
  })

  it('shows a write still running as its content', async () => {
    const { container } = await open(
      call('write', { file_path: '/tmp/notes.md', content: '# Notes\nnew line\n' }),
      undefined,
      /Write/,
    )
    expect(container.querySelector('pre')?.textContent).toContain('new line')
  })
})
