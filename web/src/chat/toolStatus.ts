import type { SessionStatus } from '../api/wire'
import type { FinishPart, ToolResultPart } from './parts'

export type ToolStatus = 'running' | 'done' | 'error' | 'interrupted'

// interruptedResultPrefix starts the result the agent's settle step writes
// for a call that never answered (internal/agent/settle.go,
// interruptedToolCallResult). It is an error result, but the call did not
// fail: it was cut off.
export const interruptedResultPrefix = 'tool call was interrupted'

// toolStatus is the one rule for a call's state. A result decides it when
// there is one. Without a result the call runs only while its own turn is
// live: a canceled or failed turn, or a turn the session has moved past,
// leaves it interrupted rather than spinning forever.
export function toolStatus(
  result: ToolResultPart | undefined,
  finish: FinishPart | null,
  turnActive: boolean,
): ToolStatus {
  if (result !== undefined) {
    if (result.isError && result.content.startsWith(interruptedResultPrefix)) {
      return 'interrupted'
    }
    if (metadataFlag(result.metadata, 'interrupted')) {
      return 'interrupted'
    }
    return result.isError ? 'error' : 'done'
  }
  if (finish !== null && (finish.reason === 'canceled' || finish.reason === 'error')) {
    return 'interrupted'
  }
  return turnActive ? 'running' : 'interrupted'
}

// isTurnActive reports whether a session can still be working on its last
// turn: a run is in flight, or it waits on a permission or a question.
export function isTurnActive(status: SessionStatus | undefined): boolean {
  return status === 'running' || status === 'pending_interaction'
}

function metadataFlag(metadata: string, key: string): boolean {
  if (metadata === '') {
    return false
  }
  try {
    const value: unknown = JSON.parse(metadata)
    return typeof value === 'object' && value !== null && (value as Record<string, unknown>)[key] === true
  } catch {
    return false
  }
}
