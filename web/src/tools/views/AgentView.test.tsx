import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { StrictMode } from 'react'
import { describe, expect, it, vi } from 'vitest'

import { bootstrapSchema, type ParsedFrame, type WireMessage } from '../../api/wire'
import { memoryStorage } from '../../app/storage'
import type { ToolCallPart, ToolResultPart } from '../../chat/parts'
import { renderTranscript } from '../../chat/Transcript'
import { createAppStore } from '../../store/app'
import { ToolEnvironmentProvider } from '../environment'
import { toolView } from '../registry'
import ToolCall from '../ToolCall'
import { agentView, childSessionId, dispatchView, maxAgentDepth, researchView } from './AgentView'

const call: ToolCallPart = { id: 'tc-agent', name: 'agent', input: '{"prompt":"Map the parser package"}', finished: true }
const child = childSessionId('a2', 'tc-agent')

function store() {
  const created = createAppStore(memoryStorage())
  created.applyBootstrap(
    bootstrapSchema.parse({
      version: 't',
      working_dir: '/w',
      current_session_id: 's1',
      sessions: [{ id: 's1', parent_session_id: '', title: 'Top', message_count: 1, updated_at: 1, status: 'running' }],
      model: { id: 'm', name: 'M', provider: 'p', context_window: 1000, agent_ready: true },
      agent_ready: true,
      permission_mode: 'ask',
      input_mode: 'code',
      lsp: {},
      mcp: {},
      agent_runs: [],
    }),
  )
  return created
}

function childSnapshot(messages: WireMessage[]): ParsedFrame {
  return {
    seq: 2,
    type: 'session_snapshot',
    sessionId: child,
    payload: {
      session_id: child,
      session: { id: child, parent_session_id: 's1', title: 'Sub', message_count: messages.length, updated_at: 1 },
      messages,
      has_more: false,
      busy: true,
      queued: [],
      files: [],
    },
  }
}

function childError(code: string, message: string): ParsedFrame {
  return { seq: 3, type: 'web_error', payload: { session_id: child, code, error: message } }
}

const childCall: WireMessage = {
  id: 'c1',
  role: 'assistant',
  session_id: child,
  parts: [{ type: 'tool_call', data: { id: 'tc-child', name: 'view', input: '{"file_path":"/tmp/project/lexer.go"}', finished: true } }],
  created_at: 1,
  updated_at: 1,
}

interface HarnessProps {
  chat: ReturnType<typeof store>
  call: ToolCallPart
  result?: ToolResultPart
  depth?: number
  // show takes the row away and brings it back, which is how the
  // virtualized log remounts one.
  show?: boolean
  strict?: boolean
}

function Harness({ chat, call, result, depth = 0, show = true, strict = false }: HarnessProps) {
  const tree = (
    <ToolEnvironmentProvider store={chat} renderTranscript={renderTranscript}>
      <ul>
        {show ? (
          <ToolCall call={call} result={result} status={result ? 'done' : 'running'} messageId="a2" depth={depth} />
        ) : null}
      </ul>
    </ToolEnvironmentProvider>
  )
  return strict ? <StrictMode>{tree}</StrictMode> : tree
}

function view(chat = store(), result?: ToolResultPart) {
  render(<Harness chat={chat} call={call} result={result} />)
  return chat
}

function lines(count: number): string {
  return Array.from({ length: count }, (_, index) => `line ${index + 1}`).join('\n')
}

describe('the agent view', () => {
  it('builds the subagent session id the coordinator uses', () => {
    expect(child).toBe('a2$$tc-agent')
    // Past the last level the tree stops nesting, and the answer is all a
    // row shows.
    expect(maxAgentDepth).toBe(3)
  })

  it('summarises the call by its prompt', () => {
    view()
    expect(screen.getByRole('button', { name: /Agent/ })).toHaveTextContent('Map the parser package')
  })

  it('watches the child while open and draws its calls as a nested tree', async () => {
    const chat = view()
    await userEvent.click(screen.getByRole('button', { name: /Agent/ }))
    expect(chat.get().watchedChildren).toEqual([child])
    expect(screen.getByText('Loading the subagent…')).toBeInTheDocument()
    act(() => chat.applyFrame(childSnapshot([childCall])))
    const nested = screen.getByRole('button', { name: /Read/ })
    expect(nested).toHaveTextContent('/tmp/project/lexer.go')
    expect(nested).toHaveTextContent('Running')
  })

  it('unwatches the child when the row closes', async () => {
    const chat = view()
    const header = screen.getByRole('button', { name: /Agent/ })
    await userEvent.click(header)
    await userEvent.click(header)
    expect(chat.get().watchedChildren).toEqual([])
  })

  it('shows the final answer as Markdown', async () => {
    view(store(), {
      toolCallId: 'tc-agent',
      name: 'agent',
      content: 'The parser has **two** files.',
      mimeType: '',
      metadata: '',
      isError: false,
    })
    await userEvent.click(screen.getByRole('button', { name: /Agent/ }))
    expect(screen.getByText('two').tagName).toBe('STRONG')
  })

  it('watches only once the call has started, so a row opened early still resolves', async () => {
    const chat = store()
    const { rerender } = render(<Harness chat={chat} call={{ ...call, finished: false }} />)
    await userEvent.click(screen.getByRole('button', { name: /Agent/ }))
    // The call has not run yet: its session cannot exist, so a watch here
    // would answer unknown_session and be lost.
    expect(chat.get().watchedChildren).toEqual([])
    expect(screen.queryByText('Loading the subagent…')).toBeNull()

    rerender(<Harness chat={chat} call={{ ...call, finished: true }} />)
    expect(chat.get().watchedChildren).toEqual([child])
    act(() => chat.applyFrame(childSnapshot([childCall])))
    expect(screen.getByRole('button', { name: /Read/ })).toHaveTextContent('/tmp/project/lexer.go')
  })

  it('surfaces a gateway error about the child on the parent row', async () => {
    const chat = store()
    render(<Harness chat={chat} call={call} />)
    await userEvent.click(screen.getByRole('button', { name: /Agent/ }))
    expect(screen.queryByRole('alert')).toBeNull()

    act(() => chat.applyFrame(childError('unknown_session', 'no such session')))
    expect(screen.getByRole('alert')).toHaveTextContent('The subagent could not be loaded: no such session')
  })

  it('watches once under StrictMode and unwatches on the final unmount', async () => {
    const chat = store()
    const watch = vi.spyOn(chat, 'watchChild')
    const unwatch = vi.spyOn(chat, 'unwatchChild')
    const { unmount } = render(<Harness chat={chat} call={call} strict />)
    await userEvent.click(screen.getByRole('button', { name: /Agent/ }))
    // StrictMode mount, cleanup, mount: the row must still be watching.
    expect(chat.get().watchedChildren).toEqual([child])
    expect(watch).toHaveBeenCalledTimes(2)

    unmount()
    expect(chat.get().watchedChildren).toEqual([])
    expect(unwatch).toHaveBeenCalledTimes(2)
    // One unwatch per watch: the row never leaves a subscription behind.
    expect(unwatch.mock.calls.length).toBe(watch.mock.calls.length)
  })

  it('stops nesting at the deepest level and shows the answer alone', async () => {
    const chat = store()
    const answer: ToolResultPart = {
      toolCallId: 'tc-agent',
      name: 'agent',
      content: 'The **final** answer.',
      mimeType: '',
      metadata: '',
      isError: false,
    }
    render(<Harness chat={chat} call={call} result={answer} depth={maxAgentDepth} />)
    await userEvent.click(screen.getByRole('button', { name: /Agent/ }))
    expect(chat.get().watchedChildren).toEqual([])
    expect(screen.queryByText('Loading the subagent…')).toBeNull()
    expect(screen.getByText('final').tagName).toBe('STRONG')
  })

  it('nests one level above the deepest level', async () => {
    const chat = store()
    render(<Harness chat={chat} call={call} depth={maxAgentDepth - 1} />)
    await userEvent.click(screen.getByRole('button', { name: /Agent/ }))
    expect(chat.get().watchedChildren).toEqual([child])
  })

  it('keeps an error the person revealed whole when the row remounts', async () => {
    const chat = store()
    const failed: ToolResultPart = {
      toolCallId: 'tc-agent',
      name: 'agent',
      content: lines(50),
      mimeType: '',
      metadata: '',
      isError: true,
    }
    const { rerender } = render(<Harness chat={chat} call={call} result={failed} />)
    await userEvent.click(screen.getByRole('button', { name: /Agent/ }))
    await userEvent.click(screen.getByRole('button', { name: 'Show all 50 lines' }))
    expect(screen.getByText(/line 50/)).toBeInTheDocument()

    rerender(<Harness chat={chat} call={call} result={failed} show={false} />)
    rerender(<Harness chat={chat} call={call} result={failed} />)
    expect(screen.queryByRole('button', { name: /Show all/ })).toBeNull()
    expect(screen.getByText(/line 50/)).toBeInTheDocument()
  })

  it('gives agentic_fetch and agent_dispatch rows of their own', () => {
    expect(toolView('agent')).toBe(agentView)
    expect(toolView('agentic_fetch')).toBe(researchView)
    expect(toolView('agent_dispatch')).toBe(dispatchView)
    expect(researchView.summary({ url: 'https://example.com/a', prompt: 'summarise' }, undefined)).toBe(
      'https://example.com/a',
    )
    expect(researchView.summary({ prompt: 'summarise this page' }, undefined)).toBe('summarise this page')
    expect(dispatchView.summary({ prompt: 'Audit the parser' }, undefined)).toBe('Audit the parser')
  })
})
