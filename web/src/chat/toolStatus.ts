import type { SessionStatus } from '../api/wire'
import type { FinishPart, ToolResultPart } from './parts'

export type ToolStatus = 'running' | 'done' | 'error' | 'interrupted'

// interruptedResultPrefix starts the result the agent's settle step writes
// for a call that never answered (internal/agent/settle.go,
// interruptedToolCallResult). It is an error result, but the call did not
// fail: it was cut off.
export const interruptedResultPrefix = 'tool call was interrupted'

// interruptedErrorKind is the kind the agent stamps on every result its
// settle step writes (internal/agent/settle.go, turnEndReason, and
// internal/agent/tools/error_kind.go): a cancel, a failed run, a truncated
// or refused response, an abandoned call. Those contents are turn-end
// sentences, not the older prefix, so the kind is what says the call was
// cut off.
const interruptedErrorKind = 'interrupted'

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
    const metadata = metadataObject(result.metadata)
    if (metadataString(metadata, 'error_kind') === interruptedErrorKind) {
      return 'interrupted'
    }
    // Older transcripts carry the prefix in the content and no kind.
    if (result.isError && result.content.startsWith(interruptedResultPrefix)) {
      return 'interrupted'
    }
    // An aborted bash command marks its own result.
    if (metadata?.interrupted === true) {
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

// metadataObject reads a result's metadata string. An empty or malformed
// string answers null: the metadata is advisory, never a reason to drop the
// result itself.
function metadataObject(metadata: string): Record<string, unknown> | null {
  if (metadata === '') {
    return null
  }
  try {
    const value: unknown = JSON.parse(metadata)
    if (typeof value !== 'object' || value === null || Array.isArray(value)) {
      return null
    }
    return value as Record<string, unknown>
  } catch {
    return null
  }
}

function metadataString(metadata: Record<string, unknown> | null, key: string): string {
  const value = metadata?.[key]
  return typeof value === 'string' ? value : ''
}
