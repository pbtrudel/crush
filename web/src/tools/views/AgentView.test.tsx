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

// runOf is one row of the run registry, the shape the gateway reports in a
// bootstrap and in every agent_runs frame (internal/agentrun/registry.go).
function runOf(sessionID: string) {
  return {
    id: sessionID,
    kind: 'subagent',
    name: 'Map the parser',
    session_id: sessionID,
    parent_session_id: 's1',
    status: 'running',
    started_at: '2026-11-22T12:00:00Z',
  }
}

interface StoreOptions {
  // listed puts the child in the bootstrap's session list, which a gateway
  // does not do today but a reload may.
  listed?: boolean
  // runs lists the child among the live agent runs, which is what a tab that
  // reloaded while the subagent was running really sees.
  runs?: boolean
}

function store({ listed = false, runs = false }: StoreOptions = {}) {
  const created = createAppStore(memoryStorage())
  created.applyBootstrap(
    bootstrapSchema.parse({
      version: 't',
      working_dir: '/w',
      current_session_id: 's1',
      sessions: [
        { id: 's1', parent_session_id: '', title: 'Top', message_count: 1, updated_at: 1, status: 'running' },
        ...(listed
          ? [{ id: child, parent_session_id: 's1', title: 'Sub', message_count: 0, updated_at: 1, status: 'running' }]
          : []),
      ],
      model: { id: 'm', name: 'M', provider: 'p', context_window: 1000, agent_ready: true },
      agent_ready: true,
      permission_mode: 'ask',
      input_mode: 'code',
      lsp: {},
      mcp: {},
      agent_runs: runs ? [runOf(child)] : [],
    }),
  )
  return created
}

// runsFrame is the global frame the gateway publishes when its run list
// changes.
function runsFrame(sessionID: string): ParsedFrame {
  return { seq: 6, type: 'agent_runs', payload: [runOf(sessionID)] }
}

// childCreated is the global session frame the gateway publishes when it
// creates a subagent's session.
function childCreated(): ParsedFrame {
  return {
    seq: 2,
    type: 'session',
    payload: {
      type: 'created',
      payload: { id: child, parent_session_id: 's1', title: 'Sub', message_count: 0, updated_at: 1 },
    },
  }
}

function childSnapshot(messages: WireMessage[], busy = true): ParsedFrame {
  return {
    seq: 3,
    type: 'session_snapshot',
    sessionId: child,
    payload: {
      session_id: child,
      session: { id: child, parent_session_id: 's1', title: 'Sub', message_count: messages.length, updated_at: 1 },
      messages,
      has_more: false,
      busy,
      queued: [],
      files: [],
    },
  }
}

function childStatus(status: string): ParsedFrame {
  return { seq: 4, type: 'session_status', payload: { session_id: child, status } }
}

function childError(code: string, message: string): ParsedFrame {
  return { seq: 5, type: 'web_error', payload: { session_id: child, code, error: message } }
}

const childCall: WireMessage = {
  id: 'c1',
  role: 'assistant',
  session_id: child,
  parts: [{ type: 'tool_call', data: { id: 'tc-child', name: 'view', input: '{"file_path":"/tmp/project/lexer.go"}', finished: true } }],
  created_at: 1,
  updated_at: 1,
}

// childCalls is a child turn with one answered call and one still open.
const childCalls: WireMessage = {
  id: 'c1',
  role: 'assistant',
  session_id: child,
  parts: [
    { type: 'tool_call', data: { id: 'tc-read', name: 'view', input: '{"file_path":"/tmp/project/lexer.go"}', finished: true } },
    { type: 'tool_call', data: { id: 'tc-grep', name: 'grep', input: '{"pattern":"func Parse"}', finished: true } },
  ],
  created_at: 1,
  updated_at: 1,
}

const childAnswer: WireMessage = {
  id: 'c2',
  role: 'assistant',
  session_id: child,
  parts: [{ type: 'tool_result', data: { tool_call_id: 'tc-read', name: 'view', content: 'package parser' } }],
  created_at: 2,
  updated_at: 2,
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

function answer(content: string, isError = false): ToolResultPart {
  return { toolCallId: 'tc-agent', name: 'agent', content, mimeType: '', metadata: '', isError }
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
    // The gateway announces the child before the person opens its row; a row
    // that has no such news yet waits (see the early-open case).
    act(() => chat.applyFrame(childCreated()))
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
    act(() => chat.applyFrame(childCreated()))
    const header = screen.getByRole('button', { name: /Agent/ })
    await userEvent.click(header)
    await userEvent.click(header)
    expect(chat.get().watchedChildren).toEqual([])
  })

  it('shows the final answer as Markdown', async () => {
    view(store(), answer('The parser has **two** files.'))
    await userEvent.click(screen.getByRole('button', { name: /Agent/ }))
    expect(screen.getByText('two').tagName).toBe('STRONG')
  })

  it('waits for the child session frame before watching, then streams', async () => {
    const chat = store()
    render(<Harness chat={chat} call={call} />)
    await userEvent.click(screen.getByRole('button', { name: /Agent/ }))
    // The call has started streaming, but nothing says its session exists:
    // a watch here answers unknown_session and is never retried.
    expect(chat.get().watchedChildren).toEqual([])
    expect(screen.queryByText('Loading the subagent…')).toBeNull()

    act(() => chat.applyFrame(childCreated()))
    expect(chat.get().watchedChildren).toEqual([child])
    expect(screen.getByText('Loading the subagent…')).toBeInTheDocument()

    act(() => chat.applyFrame(childSnapshot([childCall])))
    expect(screen.getByRole('button', { name: /Read/ })).toHaveTextContent('/tmp/project/lexer.go')
  })

  it('watches a child a reload already listed, as soon as the row opens', async () => {
    const chat = store({ listed: true })
    render(<Harness chat={chat} call={call} />)
    await userEvent.click(screen.getByRole('button', { name: /Agent/ }))
    expect(chat.get().watchedChildren).toEqual([child])
  })

  it('watches a child a reload only reports in its agent runs', async () => {
    const chat = store({ runs: true })
    render(<Harness chat={chat} call={call} />)
    await userEvent.click(screen.getByRole('button', { name: /Agent/ }))
    expect(chat.get().watchedChildren).toEqual([child])
    act(() => chat.applyFrame(childSnapshot([childCall])))
    expect(screen.getByRole('button', { name: /Read/ })).toHaveTextContent('/tmp/project/lexer.go')
  })

  it('marks a child known from a live run frame', async () => {
    const chat = store()
    render(<Harness chat={chat} call={call} />)
    await userEvent.click(screen.getByRole('button', { name: /Agent/ }))
    expect(chat.get().watchedChildren).toEqual([])
    act(() => chat.applyFrame(runsFrame(child)))
    expect(chat.get().watchedChildren).toEqual([child])
  })

  it('shows a refused research answer without a red alert', async () => {
    const chat = store()
    render(<Harness chat={chat} call={{ ...call, name: 'agentic_fetch' }} result={answer('Search is blocked.')} />)
    await userEvent.click(screen.getByRole('button', { name: /Research/ }))
    // The answer is a reason to try the watch: a settled run may have left no
    // trace in any list the reload saw.
    expect(chat.get().watchedChildren).toEqual([child])
    act(() => chat.applyFrame(childError('unknown_session', 'no such session')))
    // A fetch that never ran makes no session, and that is not news when
    // there is an answer.
    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.getByText('Search is blocked.')).toBeInTheDocument()
    // The refusal drops the watch, so it is not retried until the child is
    // announced.
    expect(chat.get().watchedChildren).toEqual([])
  })

  it('watches a settled row the reload knows nothing about', async () => {
    const chat = store()
    render(<Harness chat={chat} call={call} result={answer('The parser has **two** files.')} />)
    await userEvent.click(screen.getByRole('button', { name: /Agent/ }))
    // No run is listed and no frame named the child: its answer is the only
    // thing left, and the gateway can still resolve the persisted session.
    expect(chat.get().watchedChildren).toEqual([child])
    act(() => chat.applyFrame(childSnapshot([childCall])))
    expect(screen.getByRole('button', { name: /Read/ })).toHaveTextContent('/tmp/project/lexer.go')
  })

  it('re-watches a dispatch child the gateway refused once a run names it', async () => {
    const chat = store()
    render(
      <Harness chat={chat} call={{ ...call, name: 'agent_dispatch' }} result={answer('Started subagent "audit".')} />,
    )
    await userEvent.click(screen.getByRole('button', { name: /Agent/ }))
    expect(chat.get().watchedChildren).toEqual([child])
    act(() => chat.applyFrame(childError('unknown_session', 'no such session')))
    expect(chat.get().watchedChildren).toEqual([])
    expect(screen.queryByRole('alert')).toBeNull()

    // The gateway only reports the run once the session exists; the row takes
    // the watch again without being closed and reopened.
    act(() => chat.applyFrame(runsFrame(child)))
    expect(chat.get().watchedChildren).toEqual([child])
    act(() => chat.applyFrame(childSnapshot([childCall])))
    expect(screen.getByRole('button', { name: /Read/ })).toHaveTextContent('/tmp/project/lexer.go')
  })

  it('names a refusal that is not a missing session, answer or not', async () => {
    const chat = store()
    render(
      <Harness chat={chat} call={{ ...call, name: 'agent_dispatch' }} result={answer('Started subagent "audit".')} />,
    )
    await userEvent.click(screen.getByRole('button', { name: /Agent/ }))
    act(() => chat.applyFrame(childError('too_many_subscriptions', 'this connection holds the maximum')))
    // The session exists; only this connection is full, so the row says so
    // instead of waiting on a snapshot that will never come.
    expect(screen.getByRole('alert')).toHaveTextContent('this connection holds the maximum')
    expect(screen.queryByText('Loading the subagent…')).toBeNull()
    expect(chat.get().watchedChildren).toEqual([child])
  })

  it('surfaces a gateway error about the child on the parent row', async () => {
    const chat = store()
    render(<Harness chat={chat} call={call} />)
    act(() => chat.applyFrame(childCreated()))
    await userEvent.click(screen.getByRole('button', { name: /Agent/ }))
    expect(screen.queryByRole('alert')).toBeNull()

    act(() => chat.applyFrame(childError('unknown_session', 'no such session')))
    expect(screen.getByRole('alert')).toHaveTextContent('The subagent could not be loaded: no such session')
  })

  it("stops naming a child's refusal once its snapshot lands", async () => {
    const chat = store()
    render(<Harness chat={chat} call={call} />)
    const header = screen.getByRole('button', { name: /Agent/ })
    act(() => chat.applyFrame(childCreated()))
    await userEvent.click(header)
    // A session the gateway has but cannot read: the watch stays, so the
    // retried read can still land.
    act(() => chat.applyFrame(childError('snapshot_failed', 'the session could not be read')))
    expect(screen.getByRole('alert')).toBeInTheDocument()

    act(() => chat.applyFrame(childSnapshot([childCall])))
    expect(screen.queryByRole('alert')).toBeNull()
    expect(chat.get().error).toBeNull()

    // Closing and opening again shows the transcript, not the old refusal.
    await userEvent.click(header)
    await userEvent.click(header)
    expect(screen.queryByRole('alert')).toBeNull()
    act(() => chat.applyFrame(childSnapshot([childCall])))
    expect(screen.getByRole('button', { name: /Read/ })).toBeInTheDocument()
  })

  it("reads the child's own run state, not the parent call's", async () => {
    const chat = store()
    render(
      <Harness chat={chat} call={{ ...call, name: 'agent_dispatch' }} result={answer('Started subagent "parser-audit".')} />,
    )
    await userEvent.click(screen.getByRole('button', { name: /Agent/ }))
    act(() => chat.applyFrame(childCreated()))
    // The dispatch row answered at once, but the child is still working.
    act(() => chat.applyFrame(childSnapshot([childCalls, childAnswer], true)))
    expect(screen.getByRole('button', { name: /Read/ })).toHaveTextContent('Done')
    expect(screen.getByRole('button', { name: /Grep/ })).toHaveTextContent('Running')

    act(() => chat.applyFrame(childStatus('idle')))
    expect(screen.getByRole('button', { name: /Grep/ })).toHaveTextContent('Interrupted')
    expect(screen.getByRole('button', { name: /Read/ })).toHaveTextContent('Done')
  })

  it('watches once under StrictMode and unwatches on the final unmount', async () => {
    const chat = store({ listed: true })
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
    const chat = store({ listed: true })
    render(<Harness chat={chat} call={call} result={answer('The **final** answer.')} depth={maxAgentDepth} />)
    await userEvent.click(screen.getByRole('button', { name: /Agent/ }))
    expect(chat.get().watchedChildren).toEqual([])
    expect(screen.queryByText('Loading the subagent…')).toBeNull()
    expect(screen.getByText('final').tagName).toBe('STRONG')
  })

  it('nests one level above the deepest level', async () => {
    const chat = store({ listed: true })
    render(<Harness chat={chat} call={call} depth={maxAgentDepth - 1} />)
    await userEvent.click(screen.getByRole('button', { name: /Agent/ }))
    expect(chat.get().watchedChildren).toEqual([child])
  })

  it('keeps an error the person revealed whole when the row remounts', async () => {
    const chat = store()
    const failed = answer(lines(50), true)
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
