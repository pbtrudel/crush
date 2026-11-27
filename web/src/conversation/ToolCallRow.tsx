import styles from './MessageView.module.css'

export interface ToolCallRowProps {
  name: string
  done: boolean
}

// ToolCallRow is one tool call: its name and whether its result has arrived.
// The arguments and the result itself are M2's tool views.
export default function ToolCallRow({ name, done }: ToolCallRowProps) {
  return (
    <li className={styles.tool} data-testid="tool-call">
      <span className={styles.toolName}>{name}</span>
      <span className={styles.toolStatus}>{done ? 'done' : 'running'}</span>
    </li>
  )
}
