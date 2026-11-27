import { useRef } from 'react'

import type { SessionRow as SessionRowData } from '../api/wire'
import SessionRow from './SessionRow'
import styles from './Sidebar.module.css'

export interface SidebarProps {
  workspaceName: string
  branch?: string
  sessions: SessionRowData[]
  selectedSessionId: string | null
  unread(id: string): boolean
  error: string | null
  onSelect(id: string): void
  onCreate(): void
  onRename(id: string, title: string): void
  onDelete(id: string): Promise<void>
  onCopyId(id: string): void
  onOpenInTerminal(id: string): void
  onDismissError(): void
}

export default function Sidebar({
  workspaceName,
  branch,
  sessions,
  selectedSessionId,
  unread,
  error,
  onSelect,
  onCreate,
  onRename,
  onDelete,
  onCopyId,
  onOpenInTerminal,
  onDismissError,
}: SidebarProps) {
  const newSession = useRef<HTMLButtonElement>(null)

  // The error row leaves with the dismissal, so focus moves to the control
  // that started the action before the button unmounts; otherwise it falls
  // to the body.
  const dismiss = () => {
    onDismissError()
    newSession.current?.focus()
  }

  return (
    <nav className={styles.sidebar} aria-label="Sessions">
      <header className={styles.header}>
        <span className={styles.workspace}>{workspaceName}</span>
        {branch ? <span className={styles.branch}>{branch}</span> : null}
      </header>
      <button ref={newSession} type="button" className={styles.new} onClick={onCreate}>
        New Session
      </button>
      {error ? (
        <div className={styles.errorRow}>
          <p role="alert" className={styles.error}>
            {error}
          </p>
          <button type="button" className={styles.dismiss} onClick={dismiss}>
            Dismiss
          </button>
        </div>
      ) : null}
      <ul className={styles.list}>
        {sessions.map((row) => (
          <SessionRow
            key={row.id}
            row={row}
            selected={row.id === selectedSessionId}
            unread={unread(row.id)}
            onSelect={onSelect}
            onRename={onRename}
            onDelete={onDelete}
            onCopyId={onCopyId}
            onOpenInTerminal={onOpenInTerminal}
          />
        ))}
      </ul>
    </nav>
  )
}
