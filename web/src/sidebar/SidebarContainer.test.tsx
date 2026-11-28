import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import type { ApiClient } from '../api/http'
import { HttpError } from '../api/http'
import { bootstrapSchema, type SessionRow as SessionRowData } from '../api/wire'
import { createAppStore, type AppState } from '../store/app'
import { memoryStorage } from '../app/storage'
import SidebarContainer from './SidebarContainer'

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

const bootstrap = bootstrapSchema.parse({
  version: '0.1',
  working_dir: '/home/me/src/crush',
  branch: 'main',
  current_session_id: 's1',
  sessions: [],
  model: { id: 'm1', name: 'Model', provider: 'fake', context_window: 1, agent_ready: true },
  agent_ready: true,
  permission_mode: 'ask',
  input_mode: 'code',
  lsp: {},
  mcp: {},
  agent_runs: [],
})

type Send = (method: string, path: string, body?: unknown) => Promise<unknown>

function setup(seed: Partial<AppState> = {}, send: Send = async () => undefined) {
  const store = createAppStore(memoryStorage())
  store.set(seed)
  const client = { send: vi.fn(send) } as unknown as ApiClient
  render(<SidebarContainer client={client} store={store} />)
  return { store, client }
}

// rowOf finds the list item a session title belongs to, so an action can be
// clicked on the row it names.
function rowOf(title: string): HTMLElement {
  const item = screen.getByText(title).closest('li')
  if (item === null) {
    throw new Error(`no session row for ${title}`)
  }
  return item
}

// stubClipboard replaces navigator.clipboard for one test. jsdom has no
// clipboard and userEvent.setup installs its own stub, so the previous value
// is whatever was there before the replacement.
function stubClipboard(value: unknown): () => void {
  const original = Object.getOwnPropertyDescriptor(navigator, 'clipboard')
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value })
  return () => {
    if (original === undefined) {
      Reflect.deleteProperty(navigator, 'clipboard')
      return
    }
    Object.defineProperty(navigator, 'clipboard', original)
  }
}

describe('SidebarContainer', () => {
  it('names the workspace after its directory and keeps the branch', () => {
    setup({ bootstrap })
    expect(screen.getByText('crush')).toBeInTheDocument()
    expect(screen.getByText('main')).toBeInTheDocument()
  })

  it('names a Windows workspace after its last directory', () => {
    setup({ bootstrap: { ...bootstrap, working_dir: 'C:\\Users\\me\\src\\crush' } })
    expect(screen.getByText('crush')).toBeInTheDocument()
  })

  it('marks an unread session through the store, not a fresh comparison', async () => {
    setup({
      sessions: [row({ id: 's1', updated_at: 10 }), row({ id: 's2', title: 'Write parser tests', updated_at: 20 })],
      selectedSessionId: 's2',
    })
    expect(within(rowOf('Port the parser')).getByLabelText('Unread')).toBeInTheDocument()
    // The open session is never unread, however much it changed.
    expect(within(rowOf('Write parser tests')).queryByLabelText('Unread')).toBeNull()
  })

  it('selects a session at once', async () => {
    const user = userEvent.setup()
    const { store } = setup({ sessions: [row()] })
    await user.click(screen.getByText('Port the parser'))
    expect(store.get().selectedSessionId).toBe('s1')
  })

  it('creates a session and selects it right away', async () => {
    const user = userEvent.setup()
    const created = row({ id: 's9', title: 'New Session', updated_at: 30 })
    const { store, client } = setup({}, async () => created)
    await user.click(screen.getByRole('button', { name: 'New Session' }))
    expect(client.send).toHaveBeenCalledWith('POST', '/api/sessions', { title: 'New Session' })
    await waitFor(() => expect(store.get().selectedSessionId).toBe('s9'))
  })

  it('forgets a deleted session, clearing the selection on it', async () => {
    const user = userEvent.setup()
    const { store, client } = setup({
      sessions: [row(), row({ id: 's2', title: 'Write parser tests', updated_at: 20 })],
      selectedSessionId: 's2',
    })
    await user.click(within(rowOf('Write parser tests')).getByLabelText('Delete session'))
    expect(client.send).toHaveBeenCalledWith('DELETE', '/api/sessions/s2')
    await waitFor(() => expect(store.get().selectedSessionId).toBeNull())
    expect(store.get().sessions.map((session) => session.id)).toEqual(['s1'])
  })

  it('keeps a row whose delete failed and shows the refusal', async () => {
    const user = userEvent.setup()
    const { store } = setup(
      {
        sessions: [row(), row({ id: 's2', title: 'Write parser tests', updated_at: 20 })],
        selectedSessionId: 's2',
      },
      async () => {
        throw new HttpError(409, 'session_busy', 'The terminal is showing this session; switch it first')
      },
    )
    await user.click(within(rowOf('Write parser tests')).getByLabelText('Delete session'))
    expect(await screen.findByRole('alert')).toHaveTextContent('switch it first')
    expect(screen.getByText('Write parser tests')).toBeInTheDocument()
    expect(store.get().sessions).toHaveLength(2)
    expect(store.get().selectedSessionId).toBe('s2')
  })

  it('drops a refusal once a later action succeeds', async () => {
    const user = userEvent.setup()
    let refuse = true
    const { store } = setup(
      { sessions: [row()], selectedSessionId: 's1' },
      async () => {
        if (refuse) {
          throw new HttpError(409, 'session_busy', 'The terminal is showing this session; switch it first')
        }
        return undefined
      },
    )
    await user.click(screen.getByLabelText('Delete session'))
    expect(await screen.findByRole('alert')).toHaveTextContent('switch it first')
    refuse = false
    await user.click(screen.getByLabelText('Delete session'))
    await waitFor(() => expect(screen.queryByRole('alert')).toBeNull())
    expect(store.get().sessions).toEqual([])
  })

  it('dismisses a refusal without retrying anything', async () => {
    const user = userEvent.setup()
    const { store } = setup({ sessions: [row()], selectedSessionId: 's1' }, async () => {
      throw new HttpError(404, 'not_found', 'that session is gone')
    })
    await user.click(screen.getByLabelText('Delete session'))
    expect(await screen.findByRole('alert')).toHaveTextContent('that session is gone')
    await user.click(screen.getByRole('button', { name: 'Dismiss' }))
    expect(screen.queryByRole('alert')).toBeNull()
    expect(store.get().sessions).toHaveLength(1)
  })

  it('sends one delete while the first is in flight', async () => {
    const user = userEvent.setup()
    let finish: (value: unknown) => void = () => undefined
    const { client, store } = setup(
      { sessions: [row()], selectedSessionId: 's1' },
      () =>
        new Promise((resolve) => {
          finish = resolve
        }),
    )
    const button = screen.getByLabelText('Delete session')
    await user.click(button)
    await user.click(button)
    expect(client.send).toHaveBeenCalledTimes(1)
    await act(async () => {
      finish(undefined)
    })
    await waitFor(() => expect(store.get().sessions).toEqual([]))
  })

  it('shows an error when the browser has no clipboard', async () => {
    const user = userEvent.setup()
    const restore = stubClipboard(undefined)
    try {
      setup({ sessions: [row()], selectedSessionId: 's1' })
      await user.click(screen.getByLabelText('Copy session ID'))
      expect(await screen.findByRole('alert')).toHaveTextContent('copy the session ID')
    } finally {
      restore()
    }
  })

  it('shows an error when the clipboard write is refused', async () => {
    const user = userEvent.setup()
    const restore = stubClipboard({
      writeText: async () => {
        throw new Error('denied by the browser')
      },
    })
    try {
      setup({ sessions: [row()], selectedSessionId: 's1' })
      await user.click(screen.getByLabelText('Copy session ID'))
      expect(await screen.findByRole('alert')).toHaveTextContent('copy the session ID')
    } finally {
      restore()
    }
  })

  it('renames a session through the gateway', async () => {
    const user = userEvent.setup()
    const { client } = setup({ sessions: [row()], selectedSessionId: 's1' })
    await user.click(screen.getByLabelText('Rename session'))
    const input = screen.getByLabelText('Session title')
    await user.clear(input)
    await user.type(input, 'Renamed{Enter}')
    expect(client.send).toHaveBeenCalledWith('PUT', '/api/sessions/s1', { title: 'Renamed' })
  })

  it('asks the terminal to open a session', async () => {
    const user = userEvent.setup()
    const { client } = setup({ sessions: [row()], selectedSessionId: 's1' })
    await user.click(screen.getByLabelText('Open in terminal'))
    expect(client.send).toHaveBeenCalledWith('POST', '/api/follow', { session_id: 's1' })
  })
})
