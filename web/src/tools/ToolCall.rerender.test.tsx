import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import type { ToolCallPart } from '../chat/parts'
import { ToolEnvironmentProvider } from './environment'
import ToolCall from './ToolCall'

const { renderCounts, mockToolView } = vi.hoisted(() => {
  const renderCounts: Record<string, number> = {}

  // The summary runs on every render of a row, and the call's input names
  // the row, so the counter says which rows drew.
  function mockToolView(name: string) {
    return {
      title: () => name,
      summary: (input: Record<string, unknown> | null) => {
        const row = typeof input?.row === 'string' ? input.row : ''
        renderCounts[row] = (renderCounts[row] ?? 0) + 1
        return row
      },
      Body: () => null,
    }
  }

  return { renderCounts, mockToolView }
})

vi.mock('./registry', () => ({ toolView: mockToolView }))

function call(row: string): ToolCallPart {
  return { id: `tc-${row}`, name: `tool_${row}`, input: JSON.stringify({ row }), finished: true }
}

describe('ToolCall re-renders', () => {
  it('re-renders only the toggled row', async () => {
    render(
      <ToolEnvironmentProvider>
        <ul>
          {['a', 'b', 'c'].map((row) => (
            <ToolCall key={row} call={call(row)} result={undefined} status="running" messageId="m1" depth={0} />
          ))}
        </ul>
      </ToolEnvironmentProvider>,
    )
    const mounted = { ...renderCounts }
    await userEvent.click(screen.getByRole('button', { name: /tool_a/ }))
    expect(renderCounts).toEqual({ ...mounted, a: mounted.a + 1 })
    const afterA = { ...renderCounts }
    await userEvent.click(screen.getByRole('button', { name: /tool_b/ }))
    expect(renderCounts).toEqual({ ...afterA, b: afterA.b + 1 })
  })
})
