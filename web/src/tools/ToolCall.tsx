import { memo, useContext, useId, useMemo, useSyncExternalStore } from 'react'

import type { ToolCallPart, ToolResultPart } from '../chat/parts'
import type { ToolStatus } from '../chat/toolStatus'
import { ToolEnvironmentContext } from './environment'
import { parseInput } from './input'
import { toolView } from './registry'
import StatusCapsule from './StatusCapsule'
import styles from './Tools.module.css'

export interface ToolCallProps {
  call: ToolCallPart
  result: ToolResultPart | undefined
  status: ToolStatus
  messageId: string
  depth: number
}

// ToolCall is one call in the tree. The header is a real button, so the
// row opens from the keyboard; the body mounts only while it is open.
// It reads its own open flag from the environment's set, so a toggle
// re-renders this row and no other.
function ToolCall({ call, result, status, messageId, depth }: ToolCallProps) {
  const environment = useContext(ToolEnvironmentContext)
  const view = toolView(call.name)
  const input = useMemo(() => parseInput(call.input), [call.input])
  const expanded = environment.expanded
  const open = useSyncExternalStore(expanded.subscribe, () => expanded.has(call.id))
  const bodyId = useId()
  const summary = view.summary(input, result)
  const Body = view.Body
  return (
    <li className={styles.call} data-testid="tool-call" data-tool={call.name} data-status={status}>
      <button
        type="button"
        className={styles.header}
        aria-expanded={open}
        aria-controls={open ? bodyId : undefined}
        onClick={() => environment.toggle(call.id)}
      >
        <span className={styles.title}>{view.title(call.name, environment.mcpServers)}</span>
        {summary !== '' ? <span className={styles.summary}>{summary}</span> : null}
        <StatusCapsule status={status} />
      </button>
      {open ? (
        <div id={bodyId} className={styles.body}>
          <Body call={call} input={input} result={result} status={status} messageId={messageId} depth={depth} />
        </div>
      ) : null}
    </li>
  )
}

export default memo(ToolCall)
