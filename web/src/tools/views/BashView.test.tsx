import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import type { ToolCallPart, ToolResultPart } from '../../chat/parts'
import { ToolEnvironmentProvider } from '../environment'
import { toolView } from '../registry'
import ToolCall from '../ToolCall'

const call: ToolCallPart = {
  id: 'tc-1',
  name: 'bash',
  input: '{"command":"go test ./...","description":"Run the tests"}',
  finished: true,
}

function result(metadata: Record<string, unknown>, content = 'FAIL'): ToolResultPart {
  return {
    toolCallId: 'tc-1',
    name: 'bash',
    content,
    mimeType: '',
    metadata: JSON.stringify(metadata),
    isError: false,
  }
}

async function open(entry: ToolResultPart | undefined) {
  const view = render(
    <ToolEnvironmentProvider>
      <ul>
        <ToolCall call={call} result={entry} status={entry ? 'done' : 'running'} messageId="m1" depth={0} />
      </ul>
    </ToolEnvironmentProvider>,
  )
  await userEvent.click(screen.getByRole('button', { name: /Bash/ }))
  return view
}

describe('the bash view', () => {
  it('summarises the call by its description', () => {
    render(
      <ToolEnvironmentProvider>
        <ul>
          <ToolCall call={call} result={undefined} status="running" messageId="m1" depth={0} />
        </ul>
      </ToolEnvironmentProvider>,
    )
    expect(screen.getByRole('button', { name: /Bash/ })).toHaveTextContent('Run the tests')
  })

  it('shows the command, the coloured output and the exit code', async () => {
    const { container } = await open(result({ output: '\x1b[31mFAIL\x1b[0m parser', exit_code: 1 }))
    expect(screen.getByText('go test ./...')).toBeInTheDocument()
    const red = screen.getByText('FAIL')
    expect(red.className).toMatch(/fg-red/)
    expect(screen.getByText('exit 1')).toBeInTheDocument()
    expect(container.querySelector('[style]')).toBeNull()
  })

  it('falls back to the result text without metadata', async () => {
    await open({ ...result({}, 'plain output'), metadata: '' })
    expect(screen.getByText('plain output')).toBeInTheDocument()
    expect(screen.queryByText(/^exit /)).toBeNull()
  })

  it('names a background job', async () => {
    await open(result({ output: '', background: true, shell_id: '007' }, 'started'))
    expect(screen.getByText('Running in the background as job 007')).toBeInTheDocument()
  })

  it('shows the command alone while it runs', async () => {
    await open(undefined)
    expect(screen.getByText('go test ./...')).toBeInTheDocument()
  })

  it('caps a passing command green', async () => {
    await open(result({ output: 'ok', exit_code: 0 }))
    expect(screen.getByText('exit 0').className).toMatch(/done/)
  })

  it('clips a long output and keeps the reveal across a remount', async () => {
    const output = Array.from({ length: 250 }, (_, index) => `line-${index + 1}`).join('\n')
    const view = await open(result({ output }))
    expect(screen.queryByText('line-250')).toBeNull()
    await userEvent.click(screen.getByRole('button', { name: 'Show all 250 lines' }))
    expect(screen.getByText('line-250')).toBeInTheDocument()
    // The log drops the row and mounts it again: the reveal lives in the
    // environment, so the whole output is still there.
    view.rerender(
      <ToolEnvironmentProvider>
        <ul />
      </ToolEnvironmentProvider>,
    )
    view.rerender(
      <ToolEnvironmentProvider>
        <ul>
          <ToolCall call={call} result={result({ output })} status="done" messageId="m1" depth={0} />
        </ul>
      </ToolEnvironmentProvider>,
    )
    expect(screen.getByText('line-250')).toBeInTheDocument()
  })

  it('caps one enormous line and parses only what it shows', async () => {
    const output = 'x'.repeat(50000)
    const { container } = await open(result({ output }))
    // The line clip cannot cut one long line, so the character cap does,
    // and the terminal parses the clipped text rather than all of it.
    expect(container.querySelector('pre')?.textContent).toHaveLength(20000)
    const more = screen.getByRole('button', { name: 'Show all (50 K characters)' })
    await userEvent.click(more)
    expect(container.querySelector('pre')?.textContent).toHaveLength(50000)
    expect(screen.queryByRole('button', { name: /Show all/ })).toBeNull()
  })

  it('draws a background job as a terminal', async () => {
    const job: ToolCallPart = { id: 'tc-2', name: 'job_output', input: '{"shell_id":"007"}', finished: true }
    const jobResult: ToolResultPart = {
      toolCallId: 'tc-2',
      name: 'job_output',
      content: 'still going',
      mimeType: '',
      metadata: JSON.stringify({ command: 'sleep 1', shell_id: '007' }),
      isError: false,
    }
    render(
      <ToolEnvironmentProvider>
        <ul>
          <ToolCall call={job} result={jobResult} status="running" messageId="m1" depth={0} />
        </ul>
      </ToolEnvironmentProvider>,
    )
    const header = screen.getByRole('button', { name: /Job output/ })
    expect(header).toHaveTextContent('007')
    await userEvent.click(header)
    expect(screen.getByText('sleep 1')).toBeInTheDocument()
    expect(toolView('job_kill').title('job_kill', [])).toBe('Stop job')
  })
})
