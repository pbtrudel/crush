import { useMemo } from 'react'

import type { WireMessage } from '../api/wire'
import { resultIndex, resultSignature, type ToolResultPart } from './parts'

// useResults answers the log's tool results by call id, memoised on the
// result signature. The signature is the dependency on purpose: a streamed
// token makes a new messages array, and a fresh index with it would re-render
// every memoised message in the log. A result that is added, removed or
// rewritten changes the signature and the index with it.
export function useResults(messages: WireMessage[]): ReadonlyMap<string, ToolResultPart> {
  const signature = resultSignature(messages)
  // The factory reads the current messages: it runs only on a render whose
  // signature differs from the memoised one.
  return useMemo(() => resultIndex(messages), [signature])
}
