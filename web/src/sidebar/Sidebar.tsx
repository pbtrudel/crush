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
  return (
    <nav className={styles.sidebar} aria-label="Sessions">
      <header className={styles.header}>
        <span className={styles.workspace}>{workspaceName}</span>
        {branch ? <span className={styles.branch}>{branch}</span> : null}
      </header>
      <button type="button" className={styles.new} onClick={onCreate}>
        New Session
      </button>
      {error ? (
        <p role="alert" className={styles.error}>
          <span className={styles.errorText}>{error}</span>
          <button type="button" className={styles.dismiss} onClick={onDismissError}>
            Dismiss
          </button>
        </p>
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
