import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import type { ToolCallPart, ToolResultPart } from '../chat/parts'
import { ToolEnvironmentProvider } from './environment'
import OutputBlock from './OutputBlock'
import ToolCall from './ToolCall'

const call: ToolCallPart = { id: 'tc-1', name: 'mystery_tool', input: '{"target":"parser.go"}', finished: true }

function result(overrides: Partial<ToolResultPart> = {}): ToolResultPart {
  return { toolCallId: 'tc-1', name: 'mystery_tool', content: 'it worked', mimeType: '', metadata: '', isError: false, ...overrides }
}

function row(props: Partial<Parameters<typeof ToolCall>[0]> = {}) {
  return render(
    <ToolEnvironmentProvider>
      <ul>
        <ToolCall call={call} result={result()} status="done" messageId="m1" depth={0} {...props} />
      </ul>
    </ToolEnvironmentProvider>,
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
    expect(screen.getByRole('region', { name: 'Error' })).toHaveTextContent('no such file')
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
})

describe('OutputBlock', () => {
  it('clips a long output until asked', async () => {
    const text = Array.from({ length: 50 }, (_, index) => `line ${index + 1}`).join('\n')
    render(<OutputBlock text={text} maxLines={10} />)
    expect(screen.queryByText(/line 11/)).toBeNull()
    await userEvent.click(screen.getByRole('button', { name: 'Show all 50 lines' }))
    expect(screen.getByText(/line 50/)).toBeInTheDocument()
  })
})
