import styles from './MessageView.module.css'

export interface ToolCallRowProps {
  name: string
  finished: boolean
}

// ToolCallRow is one tool call: its name and whether it has finished. The
// arguments and the result are M2's tool views.
export default function ToolCallRow({ name, finished }: ToolCallRowProps) {
  return (
    <li className={styles.tool} data-testid="tool-call">
      <span className={styles.toolName}>{name}</span>
      <span className={styles.toolStatus}>{finished ? 'done' : 'running'}</span>
    </li>
  )
}
