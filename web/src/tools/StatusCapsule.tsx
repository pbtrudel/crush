import { clsx } from 'clsx'

import type { ToolStatus } from '../chat/toolStatus'
import styles from './Tools.module.css'

const labels: Record<ToolStatus, string> = {
  running: 'Running',
  done: 'Done',
  error: 'Failed',
  interrupted: 'Interrupted',
}

export default function StatusCapsule({ status }: { status: ToolStatus }) {
  return <span className={clsx(styles.capsule, styles[status])}>{labels[status]}</span>
}
