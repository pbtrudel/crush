import type { ComponentType } from 'react'

import type { ToolCallPart, ToolResultPart } from '../chat/parts'
import type { ToolStatus } from '../chat/toolStatus'
import type { ToolInput } from './input'

export interface ToolBodyProps {
  call: ToolCallPart
  input: ToolInput | null
  result: ToolResultPart | undefined
  status: ToolStatus
  // messageId is the assistant message that made the call; a subagent's
  // session id is built from it.
  messageId: string
  depth: number
}

// ToolView is one tool type's presentation: the row title, the one-line
// summary beside it, and the body an open row mounts.
export interface ToolView {
  title(name: string, mcpServers: readonly string[]): string
  summary(input: ToolInput | null, result: ToolResultPart | undefined): string
  Body: ComponentType<ToolBodyProps>
}
