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
  onDelete(id: string): Promise<void>
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
  const [deleting, setDeleting] = useState(false)

  // The draft starts from the title the row has now, not the one it mounted
  // with: a rename from another tab or the terminal lands on the prop while
  // this row is mounted, and the field must not resurrect the old title.
  const startRenaming = () => {
    setDraft(row.title)
    setRenaming(true)
  }

  // A delete disables its own button while the request is in flight, so a
  // second click cannot answer with a 404 for the session the first removed.
  const remove = async () => {
    if (deleting) {
      return
    }
    setDeleting(true)
    try {
      await onDelete(row.id)
    } catch {
      // The container turns the refusal into the sidebar's error row; the
      // row only re-enables its button.
    } finally {
      setDeleting(false)
    }
  }

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
            maxLength={200}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                commit()
              }
              if (event.key === 'Escape') {
                // The key is spent on the draft. A drawer the row sits in
                // listens for Escape on the window, so it has to see that
                // this control already acted on it.
                event.preventDefault()
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
        <button type="button" aria-label="Rename session" onClick={startRenaming}>
          Rename
        </button>
        <button type="button" aria-label="Copy session ID" onClick={() => onCopyId(row.id)}>
          Copy ID
        </button>
        <button type="button" aria-label="Open in terminal" onClick={() => onOpenInTerminal(row.id)}>
          Open in terminal
        </button>
        <button type="button" aria-label="Delete session" onClick={remove} disabled={deleting}>
          Delete
        </button>
      </div>
    </li>
  )
}
