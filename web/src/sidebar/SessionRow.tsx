import { clsx } from 'clsx'
import { useState } from 'react'

import type { SessionRow as SessionRowData } from '../api/wire'
import styles from './SessionRow.module.css'

export interface SessionRowProps {
  row: SessionRowData
  selected: boolean
  unread: boolean
  onSelect(id: string): void
  onRename(id: string, title: string): void
  onDelete(id: string): void
  onCopyId(id: string): void
  onOpenInTerminal(id: string): void
}

export default function SessionRow({
  row,
  selected,
  unread,
  onSelect,
  onRename,
  onDelete,
  onCopyId,
  onOpenInTerminal,
}: SessionRowProps) {
  const [renaming, setRenaming] = useState(false)
  const [draft, setDraft] = useState(row.title)

  // commit sends the title that changed and nothing else: the row keeps its
  // ID, its status and its position until the gateway echoes the rename.
  const commit = () => {
    setRenaming(false)
    const title = draft.trim()
    if (title !== '' && title !== row.title) {
      onRename(row.id, title)
    }
  }

  return (
    <li className={clsx(styles.row, selected && styles.selected)} data-status={row.status}>
      <div className={styles.main}>
        {renaming ? (
          <input
            aria-label="Session title"
            className={styles.input}
            value={draft}
            autoFocus
            onChange={(event) => setDraft(event.target.value)}
            onBlur={commit}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                commit()
              }
              if (event.key === 'Escape') {
                setRenaming(false)
                setDraft(row.title)
              }
            }}
          />
        ) : (
          <button type="button" className={styles.title} onClick={() => onSelect(row.id)}>
            <span data-testid="session-title">{row.title}</span>
          </button>
        )}
        <span className={styles.markers}>
          {row.status === 'running' ? <span role="img" className={styles.running} aria-label="Running" /> : null}
          {row.status === 'queued' ? <span role="img" className={styles.queued} aria-label="Queued" /> : null}
          {row.status === 'pending_interaction' ? (
            <span role="img" className={styles.waiting} aria-label="Waiting for you" />
          ) : null}
          {unread ? <span role="img" className={styles.unread} aria-label="Unread" /> : null}
        </span>
      </div>
      <div className={styles.actions}>
        <button type="button" aria-label="Rename session" onClick={() => setRenaming(true)}>
          Rename
        </button>
        <button type="button" aria-label="Copy session ID" onClick={() => onCopyId(row.id)}>
          Copy ID
        </button>
        <button type="button" aria-label="Open in terminal" onClick={() => onOpenInTerminal(row.id)}>
          Open in terminal
        </button>
        <button type="button" aria-label="Delete session" onClick={() => onDelete(row.id)}>
          Delete
        </button>
      </div>
    </li>
  )
}
