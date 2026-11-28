import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { ApiClient } from '../api/http'
import { HttpError } from '../api/http'
import { bootstrapSchema, type ParsedFrame, type SessionRow as SessionRowData } from '../api/wire'
import { createAppStore, emptySessionData, type AppState, type SessionData, type WireMessage } from '../store/app'
import { memoryStorage } from '../app/storage'
import { stubLayout } from '../test/layout'
import ConversationContainer from './ConversationContainer'

// jsdom reports a zero-height log, and the virtualizer renders no row in
// an empty viewport. The stub gives it one, so every row is in view.
let restore: () => void = () => undefined

beforeEach(() => {
  restore = stubLayout({ viewport: 600, row: 60 })
})

afterEach(() => {
  restore()
})

function row(overrides: Partial<SessionRowData> = {}): SessionRowData {
  return {
    id: 's1',
    parent_session_id: '',
    title: 'Port the parser',
    message_count: 2,
    updated_at: 10,
    status: 'idle',
    ...overrides,
  }
}

function message(id: string, value: string, role = 'assistant', sessionID = 's1'): WireMessage {
  return {
    id,
    role,
    session_id: sessionID,
    parts: [{ type: 'text', data: { text: value } }],
    created_at: 1,
    updated_at: 2,
  }
}

function data(messages: WireMessage[], hasMore = false, snapshotSeq = 0): SessionData {
  return { ...emptySessionData, messages, hasMore, snapshotSeq }
}

const bootstrap = bootstrapSchema.parse({
  version: '0.1',
  working_dir: '/home/me/src/crush',
  branch: 'main',
  current_session_id: 's1',
  sessions: [row()],
  model: { id: 'm1', name: 'Model', provider: 'fake', context_window: 1, agent_ready: true },
  agent_ready: true,
  permission_mode: 'ask',
  input_mode: 'code',
  lsp: {},
  mcp: {},
  agent_runs: [],
})

function snapshotFrame(sessionID: string, messages: WireMessage[], hasMore = true): ParsedFrame {
  return {
    seq: 1,
    type: 'session_snapshot',
    sessionId: sessionID,
    payload: { session_id: sessionID, messages, has_more: hasMore, busy: false, queued: [], files: [] },
  }
}

function createdFrame(next: WireMessage): ParsedFrame {
  return { seq: 2, type: 'message', sessionId: next.session_id, payload: { type: 'created', payload: next } }
}

type Get = (path: string) => Promise<unknown>

function setup(seed: Partial<AppState>, get: Get = async () => ({ messages: [], has_more: false })) {
  const store = createAppStore(memoryStorage())
  store.set(seed)
  const client = { get: vi.fn(get) } as unknown as ApiClient
  const view = render(<ConversationContainer client={client} store={store} />)
  return { store, client, view }
}

// articles is the log's message list in the order it renders. The copy
// action belongs to the message's frame, not to the text it draws, so its
// caption is taken out before the text is read.
function articles(): string[] {
  return screen.getAllByRole('article').map((element) => {
    const copy = element.cloneNode(true) as HTMLElement
    for (const button of copy.querySelectorAll('button[aria-label="Copy message"]')) {
      button.remove()
    }
    return copy.textContent ?? ''
  })
}

describe('ConversationContainer', () => {
  it("renders the selected session's title and messages", () => {
    setup({
      sessions: [row()],
      selectedSessionId: 's1',
      sessionData: { s1: data([message('m1', 'hello', 'user'), message('m2', 'hi there')]) },
    })
    expect(screen.getByRole('heading', { name: 'Port the parser' })).toBeInTheDocument()
    expect(articles()).toEqual(['hello', 'hi there'])
  })

  it('asks the gateway for the page before the oldest message on screen', async () => {
    const { client } = setup(
      { sessions: [row()], selectedSessionId: 's1', sessionData: { s1: data([message('m2', 'hi there')], true) } },
      async (path) => {
        if (path === '/api/sessions/s1/messages?before=m2&limit=100') {
          return { messages: [message('m1', 'hello', 'user')], has_more: false }
        }
        throw new Error(`unexpected request ${path}`)
      },
    )
    await userEvent.click(screen.getByRole('button', { name: 'Load older messages' }))
    expect(await screen.findByText('hello')).toBeInTheDocument()
    expect(client.get).toHaveBeenCalledWith('/api/sessions/s1/messages?before=m2&limit=100')
    expect(articles()).toEqual(['hello', 'hi there'])
    // The page said the history starts there, so the control goes.
    expect(screen.queryByRole('button', { name: 'Load older messages' })).toBeNull()
  })

  it('pages further back from the oldest message it just loaded', async () => {
    const { client } = setup(
      { sessions: [row()], selectedSessionId: 's1', sessionData: { s1: data([message('m2', 'hi there')], true) } },
      async (path) => {
        if (path.endsWith('before=m2&limit=100')) {
          return { messages: [message('m1', 'hello', 'user')], has_more: true }
        }
        if (path.endsWith('before=m1&limit=100')) {
          return { messages: [message('m0', 'first words', 'user')], has_more: false }
        }
        throw new Error(`unexpected request ${path}`)
      },
    )
    await userEvent.click(screen.getByRole('button', { name: 'Load older messages' }))
    await screen.findByText('hello')
    await userEvent.click(screen.getByRole('button', { name: 'Load older messages' }))
    await screen.findByText('first words')
    expect(client.get).toHaveBeenLastCalledWith('/api/sessions/s1/messages?before=m1&limit=100')
    expect(articles()).toEqual(['first words', 'hello', 'hi there'])
  })

  it('does not show a message twice when a page overlaps the snapshot', async () => {
    setup(
      {
        sessions: [row()],
        selectedSessionId: 's1',
        sessionData: { s1: data([message('m1', 'hello', 'user'), message('m2', 'hi there')], true) },
      },
      async () => ({ messages: [message('m1', 'hello', 'user'), message('m2', 'hi there')], has_more: false }),
    )
    await userEvent.click(screen.getByRole('button', { name: 'Load older messages' }))
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Loading…' })).toBeNull())
    expect(articles()).toEqual(['hello', 'hi there'])
  })

  it('hides the control when the snapshot is the whole history', () => {
    setup({ sessions: [row()], selectedSessionId: 's1', sessionData: { s1: data([message('m1', 'hello', 'user')]) } })
    expect(screen.queryByRole('button', { name: 'Load older messages' })).toBeNull()
  })

  it('disables the control while a page is in flight', async () => {
    let answer: ((page: unknown) => void) | undefined
    setup(
      { sessions: [row()], selectedSessionId: 's1', sessionData: { s1: data([message('m2', 'hi there')], true) } },
      () => new Promise((resolve) => (answer = resolve)),
    )
    await userEvent.click(screen.getByRole('button', { name: 'Load older messages' }))
    expect(screen.getByRole('button', { name: 'Loading…' })).toBeDisabled()
    await act(async () => {
      answer?.({ messages: [message('m1', 'hello', 'user')], has_more: true })
    })
    expect(await screen.findByRole('button', { name: 'Load older messages' })).toBeEnabled()
  })

  it('shows the reason a page failed and keeps what is on screen', async () => {
    setup(
      { sessions: [row()], selectedSessionId: 's1', sessionData: { s1: data([message('m2', 'hi there')], true) } },
      async () => {
        throw new HttpError(404, 'unknown_message', 'the session has no such message')
      },
    )
    await userEvent.click(screen.getByRole('button', { name: 'Load older messages' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('the session has no such message')
    expect(articles()).toEqual(['hi there'])
    expect(screen.getByRole('button', { name: 'Load older messages' })).toBeEnabled()
  })

  it('drops the loaded pages when a snapshot arrives for the session', async () => {
    const { store } = setup(
      { sessions: [row()], selectedSessionId: 's1', sessionData: { s1: data([message('m2', 'hi there')], true) } },
      async () => ({ messages: [message('m1', 'hello', 'user')], has_more: true }),
    )
    await userEvent.click(screen.getByRole('button', { name: 'Load older messages' }))
    await screen.findByText('hello')
    act(() => {
      store.applyFrame(snapshotFrame('s1', [message('m2', 'hi there'), message('m3', 'later')]))
    })
    await waitFor(() => expect(screen.queryByText('hello')).toBeNull())
    expect(articles()).toEqual(['hi there', 'later'])
    expect(screen.getByRole('button', { name: 'Load older messages' })).toBeInTheDocument()
  })

  // The header's usage is the session row's tokens and cost against the
  // window of the model the bootstrap carries.
  it('reads the usage from the session row and the model window', () => {
    const { store } = setup({
      bootstrap: { ...bootstrap, model: { ...bootstrap.model, context_window: 200000 } },
      sessions: [row({ prompt_tokens: 42000, completion_tokens: 1800, cost: 0.1234 })],
      selectedSessionId: 's1',
      sessionData: { s1: data([message('m2', 'hi there')]) },
    })
    const summary = screen.getByTestId('usage-summary')
    expect(summary).toHaveTextContent('43.8K tokens')
    expect(summary).toHaveTextContent('22% of 200K')
    expect(summary).toHaveTextContent('$0.12')

    // A usage write arrives as the session row again, and the header follows.
    act(() => {
      store.applyFrame({
        seq: 3,
        type: 'session',
        payload: { type: 'updated', payload: row({ prompt_tokens: 1000, completion_tokens: 0, cost: 0.5 }) },
      })
    })
    expect(screen.getByTestId('usage-summary')).toHaveTextContent('$0.50')
  })

  it('drops the loaded pages when a resync replaces the bootstrap', async () => {
    const { store } = setup(
      { sessions: [row()], selectedSessionId: 's1', sessionData: { s1: data([message('m2', 'hi there')], true) } },
      async () => ({ messages: [message('m1', 'hello', 'user')], has_more: true }),
    )
    await userEvent.click(screen.getByRole('button', { name: 'Load older messages' }))
    await screen.findByText('hello')
    act(() => {
      store.applyBootstrap(bootstrap)
    })
    await waitFor(() => expect(screen.queryByText('hello')).toBeNull())
    expect(articles()).toEqual(['hi there'])
    expect(screen.getByRole('button', { name: 'Load older messages' })).toBeInTheDocument()
  })

  it('drops the loaded pages when the snapshot counter moves for the same messages', async () => {
    // A resubscribe can answer the very same page: the message list is
    // identical, so only the snapshot counter says the history was rebuilt.
    const { store } = setup(
      { sessions: [row()], selectedSessionId: 's1', sessionData: { s1: data([message('m2', 'hi there')], true) } },
      async () => ({ messages: [message('m1', 'hello', 'user')], has_more: true }),
    )
    const frame = snapshotFrame('s1', [message('m2', 'hi there')])
    act(() => {
      store.applyFrame(frame)
    })
    await userEvent.click(screen.getByRole('button', { name: 'Load older messages' }))
    await screen.findByText('hello')
    act(() => {
      store.applyFrame(frame)
    })
    await waitFor(() => expect(screen.queryByText('hello')).toBeNull())
    expect(articles()).toEqual(['hi there'])
  })

  it('keeps the loaded pages when a live message frame arrives', async () => {
    const { store } = setup(
      { sessions: [row()], selectedSessionId: 's1', sessionData: { s1: data([message('m2', 'hi there')], true) } },
      async () => ({ messages: [message('m1', 'hello', 'user')], has_more: true }),
    )
    await userEvent.click(screen.getByRole('button', { name: 'Load older messages' }))
    await screen.findByText('hello')
    act(() => {
      store.applyFrame(createdFrame(message('m3', 'later')))
    })
    expect(articles()).toEqual(['hello', 'hi there', 'later'])
  })

  it('brings an idle session the tab stops following back without its pages', async () => {
    const { store } = setup(
      {
        sessions: [row(), row({ id: 's2', title: 'Other session' })],
        selectedSessionId: 's1',
        sessionData: { s1: data([message('m2', 'hi there')], true), s2: data([message('m4', 'elsewhere', 'user', 's2')]) },
      },
      async () => ({ messages: [message('m1', 'hello', 'user')], has_more: true }),
    )
    await userEvent.click(screen.getByRole('button', { name: 'Load older messages' }))
    await screen.findByText('hello')
    // The store keeps a session's pages in its entry, so what drops them is
    // the app releasing the session it unsubscribes, not the selection
    // itself: useGateway does both when the selection moves.
    act(() => {
      store.selectSession('s2')
      store.releaseSession('s1')
    })
    expect(await screen.findByText('elsewhere')).toBeInTheDocument()
    expect(screen.queryByText('hello')).toBeNull()
    act(() => {
      store.selectSession('s1')
      // The resubscribe answers a fresh snapshot, which is what puts the
      // session's messages back without the page that was loaded.
      store.applyFrame(snapshotFrame('s1', [message('m2', 'hi there')]))
    })
    expect(await screen.findByText('hi there')).toBeInTheDocument()
    expect(screen.queryByText('hello')).toBeNull()
    expect(screen.getByRole('button', { name: 'Load older messages' })).toBeInTheDocument()
  })

  it('drops a page that answers after the selection changed', async () => {
    let answer: ((page: unknown) => void) | undefined
    const { store } = setup(
      {
        sessions: [row(), row({ id: 's2', title: 'Other session' })],
        selectedSessionId: 's1',
        sessionData: { s1: data([message('m2', 'hi there')], true), s2: data([message('m4', 'elsewhere', 'user', 's2')]) },
      },
      () => new Promise((resolve) => (answer = resolve)),
    )
    await userEvent.click(screen.getByRole('button', { name: 'Load older messages' }))
    act(() => {
      store.selectSession('s2')
    })
    await act(async () => {
      answer?.({ messages: [message('m1', 'hello', 'user')], has_more: false })
    })
    expect(screen.queryByText('hello')).toBeNull()
    expect(articles()).toEqual(['elsewhere'])
  })

  it('shows a live edit to a message from a loaded older page', async () => {
    const page = { messages: [message('m0', 'first words')], has_more: false }
    const { store } = setup(
      { selectedSessionId: 's1', sessions: [row()], sessionData: { s1: data([message('m1', 'hello')], true) } },
      async () => page,
    )
    await userEvent.click(screen.getByRole('button', { name: 'Load older messages' }))
    await screen.findByText('first words')
    act(() =>
      store.applyFrame({
        seq: 9,
        type: 'message',
        sessionId: 's1',
        payload: { type: 'updated', payload: message('m0', 'first words, edited') },
      }),
    )
    expect(screen.getByText('first words, edited')).toBeInTheDocument()
    expect(articles()).toHaveLength(2)
  })

  it('drops a page that answers after a resync, before the new snapshot', async () => {
    let answer: (value: unknown) => void = () => undefined
    const { store } = setup(
      { selectedSessionId: 's1', sessions: [row()], sessionData: { s1: data([message('m1', 'hello')], true) } },
      () => new Promise((resolve) => (answer = resolve)),
    )
    await userEvent.click(screen.getByRole('button', { name: 'Load older messages' }))
    act(() => store.applyBootstrap(bootstrap))
    await act(async () => answer({ messages: [message('m0', 'stale page')], has_more: false }))
    expect(screen.queryByText('stale page')).toBeNull()
  })

  it('keeps the pages of a session that stays subscribed across a selection change', async () => {
    const { store } = setup(
      {
        // A running session stays subscribed, so the app releases nothing
        // when the selection moves: its entry, pages and all, is kept.
        sessions: [row({ status: 'running' }), row({ id: 's2', title: 'Other session' })],
        selectedSessionId: 's1',
        sessionData: { s1: data([message('m2', 'hi there')], true), s2: data([message('m4', 'elsewhere', 'user', 's2')]) },
      },
      async () => ({ messages: [message('m1', 'hello', 'user')], has_more: true }),
    )
    await userEvent.click(screen.getByRole('button', { name: 'Load older messages' }))
    await screen.findByText('hello')
    act(() => {
      store.selectSession('s2')
    })
    expect(await screen.findByText('elsewhere')).toBeInTheDocument()
    expect(screen.queryByText('hello')).toBeNull()
    act(() =>
      store.applyFrame({
        seq: 9,
        type: 'message',
        sessionId: 's1',
        payload: { type: 'updated', payload: message('m1', 'hello, edited', 'user') },
      }),
    )
    act(() => {
      store.selectSession('s1')
    })
    expect(await screen.findByText('hi there')).toBeInTheDocument()
    // The page is still there, with the edit that arrived while it was off
    // screen written into it.
    expect(screen.getByText('hello, edited')).toBeInTheDocument()
    expect(articles()).toEqual(['hello, edited', 'hi there'])
  })

  it('drops a page that answers after the app released and reopened the session', async () => {
    let answer: (value: unknown) => void = () => undefined
    const { store } = setup(
      { selectedSessionId: 's1', sessions: [row(), row({ id: 's2', title: 'Other session' })], sessionData: {} },
      () => new Promise((resolve) => (answer = resolve)),
    )
    // The store opens the entry, so the generation the page is asked under
    // is one the store itself handed out.
    act(() => store.applyFrame(snapshotFrame('s1', [message('m1', 'hello')])))
    await userEvent.click(screen.getByRole('button', { name: 'Load older messages' }))
    act(() => {
      // useGateway unsubscribes the session the selection left and releases
      // it, then the resubscribe opens a new entry for the same session.
      store.selectSession('s2')
      store.releaseSession('s1')
      store.applyFrame(snapshotFrame('s1', [message('m1', 'hello')]))
      store.selectSession('s1')
    })
    await act(async () => answer({ messages: [message('m0', 'stale page')], has_more: false }))
    expect(screen.queryByText('stale page')).toBeNull()
    expect(store.get().sessionData.s1.older).toEqual([])
  })

  it('renders the pane for a session the list does not carry yet', () => {
    setup({ sessions: [], selectedSessionId: 's9', sessionData: {} })
    expect(screen.getByRole('region', { name: 'Conversation' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Untitled session' })).toBeInTheDocument()
    expect(screen.queryByText('Select a session')).toBeNull()
    expect(screen.queryAllByRole('article')).toHaveLength(0)
  })
})
