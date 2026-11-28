import { clsx } from 'clsx'

import styles from './Tools.module.css'

// ExitCode is the command's exit status as a capsule: green for zero.
export default function ExitCode({ code }: { code: number }) {
  return <span className={clsx(styles.capsule, code === 0 ? styles.done : styles.error)}>exit {code}</span>
}
