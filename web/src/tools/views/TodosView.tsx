import { useCallback, useContext, useSyncExternalStore } from 'react'

import { ToolEnvironmentContext } from '../environment'
import { arrayField, numberField, parseInput, stringField, type ToolInput } from '../input'
import shared from '../Tools.module.css'
import type { ToolBodyProps, ToolView } from '../view'
import styles from './TodosView.module.css'

// maxTodos bounds a collapsed list. The list is model-authored and can be
// any length, so forty rows show before the reveal, the bound a tool's text
// output gets in lines.
export const maxTodos = 40

// maxTodoChars bounds one collapsed row's text. A single todo's content is
// model-authored too, and the reveal shows the whole text with the list.
export const maxTodoChars = 500

interface Todo {
  content: string
  status: string
  activeForm: string
}

const glyphs = new Map([
  ['completed', '✓'],
  ['in_progress', '●'],
  ['pending', '○'],
])

// clipText cuts a row's text to max UTF-16 code units, ellipsis included.
// It never keeps half a surrogate pair: that half renders as a replacement
// glyph.
function clipText(text: string, max: number): string {
  if (text.length <= max) {
    return text
  }
  let end = max - 1
  const last = text.charCodeAt(end - 1)
  if (last >= 0xd800 && last <= 0xdbff) {
    end -= 1
  }
  return `${text.slice(0, end)}…`
}

// todosOf prefers the metadata's list, which is the list after the call,
// and falls back to the call's own argument while it runs.
function todosOf(input: ToolInput | null, metadata: ToolInput | null): Todo[] {
  const source = arrayField(metadata, 'todos').length > 0 ? arrayField(metadata, 'todos') : arrayField(input, 'todos')
  return source.flatMap((entry) => {
    if (typeof entry !== 'object' || entry === null) {
      return []
    }
    const todo = entry as ToolInput
    return [
      {
        content: stringField(todo, 'content'),
        status: stringField(todo, 'status'),
        activeForm: stringField(todo, 'active_form'),
      },
    ]
  })
}

// rowText is what one row says: a todo in progress reads in its continuous
// form, every other state reads its content.
function rowText(todo: Todo): string {
  return todo.status === 'in_progress' && todo.activeForm !== '' ? todo.activeForm : todo.content
}

function TodosBody({ call, input, result }: ToolBodyProps) {
  const environment = useContext(ToolEnvironmentContext)
  const revealKey = `${call.id}:todos`
  const readRevealed = useCallback(() => environment.revealed.has(revealKey), [environment, revealKey])
  const revealed = useSyncExternalStore(environment.revealed.subscribe, readRevealed)
  const todos = todosOf(input, parseInput(result?.metadata ?? ''))
  const clipped = !revealed && todos.length > maxTodos
  const shown = clipped ? todos.slice(0, maxTodos) : todos
  return (
    <>
      <ul className={styles.todos}>
        {shown.map((todo, index) => {
          const text = rowText(todo)
          return (
            <li key={index} className={styles[todo.status] ?? styles.pending} data-status={todo.status}>
              <span className={styles.glyph} aria-hidden="true">
                {glyphs.get(todo.status) ?? '○'}
              </span>
              <span>{revealed ? text : clipText(text, maxTodoChars)}</span>
            </li>
          )
        })}
      </ul>
      {clipped ? (
        <button type="button" className={shared.more} onClick={() => environment.reveal(revealKey)}>
          Show all {todos.length} todos
        </button>
      ) : null}
    </>
  )
}

export const todosView: ToolView = {
  title: () => 'Todos',
  summary: (input, result) => {
    const metadata = parseInput(result?.metadata ?? '')
    const todos = todosOf(input, metadata)
    const done = numberField(metadata, 'completed') ?? todos.filter((todo) => todo.status === 'completed').length
    const total = numberField(metadata, 'total') ?? todos.length
    return total === 0 ? '' : `${done}/${total} done`
  },
  Body: TodosBody,
}
