import { clsx } from 'clsx'
import { useContext, useEffect, useSyncExternalStore, type ReactNode } from 'react'

import type { SessionStatus, WebError } from '../../api/wire'
import Markdown from '../../chat/markdown/Markdown'
import { isTurnActive } from '../../chat/toolStatus'
import type { WireMessage } from '../../store/app'
import { ToolEnvironmentContext, type ChatStore } from '../environment'
import { firstLine, stringField } from '../input'
import OutputBlock, { ToolSection } from '../OutputBlock'
import styles from '../Tools.module.css'
import type { ToolBodyProps, ToolView } from '../view'

// unknownSessionCode is the gateway's answer to a subscribe for a session it
// does not have (internal/web/stream.go handleSubscribe).
const unknownSessionCode = 'unknown_session'

// maxAgentDepth stops the tree: a subagent's subagent's subagent shows its
// answer only, so one runaway delegation cannot nest the log forever.
export const maxAgentDepth = 3

// childSessionId builds a subagent session's id the way the coordinator
// does: session.Service.CreateAgentToolSessionID answers
// "<message id>$$<tool call id>", and every tool that runs a subagent
// (`agent`, `agentic_fetch`, `agent_dispatch`) goes through it.
export function childSessionId(messageId: string, callId: string): string {
  return `${messageId}$$${callId}`
}

// noSubscribe is the null store's subscription: a row rendered without the
// environment's store reads nothing and watches nothing.
const noSubscribe = () => () => undefined

function noMessages(): undefined {
  return undefined
}

function noError(): null {
  return null
}

function neverKnown(): false {
  return false
}

function noStatus(): undefined {
  return undefined
}

// useChildMessages follows a watched child's snapshot and live messages. The
// store keeps them in the session entry the watch opens, so the row reads the
// same data any session has. The selector answers the entry's own array, so
// an unrelated store change keeps the identity and the transcript with it.
function useChildMessages(store: ChatStore | null, sessionID: string): WireMessage[] | undefined {
  return useSyncExternalStore(
    store === null ? noSubscribe : store.subscribe,
    () => store?.get().sessionData[sessionID]?.messages,
    noMessages,
  )
}

// useKnownChild answers whether the gateway has announced this child's
// session. A watch sent before then answers unknown_session and is never
// retried, so it is the row's signal that a subscription can be taken.
function useKnownChild(store: ChatStore | null, sessionID: string): boolean {
  return useSyncExternalStore(
    store === null ? noSubscribe : store.subscribe,
    () => store?.get().knownChildren.includes(sessionID) === true,
    neverKnown,
  )
}

// useRefusedChild answers whether the gateway answered unknown_session for
// this child's watch. The store drops the watch then, so the row must stop
// asking until a frame announces the child.
function useRefusedChild(store: ChatStore | null, sessionID: string): boolean {
  return useSyncExternalStore(
    store === null ? noSubscribe : store.subscribe,
    () => store?.get().refusedChildren.includes(sessionID) === true,
    neverKnown,
  )
}

// useChildStatus follows the child's own run state. A subagent keeps working
// after its parent's tool row answered, so the row's status is not the
// child's.
function useChildStatus(store: ChatStore | null, sessionID: string): SessionStatus | undefined {
  return useSyncExternalStore(
    store === null ? noSubscribe : store.subscribe,
    () => store?.get().childStatuses[sessionID],
    noStatus,
  )
}

// useChildError answers the gateway's last web_error while it concerns this
// child. The tab shows an error only for the session on screen, so an error
// about a subagent session is named on its row or nowhere.
function useChildError(store: ChatStore | null, sessionID: string): WebError | null {
  return useSyncExternalStore(
    store === null ? noSubscribe : store.subscribe,
    () => {
      const error = store?.get().error
      return error !== null && error !== undefined && error.session_id === sessionID ? error : null
    },
    noError,
  )
}

function AgentBody({ call, result, status, messageId, depth }: ToolBodyProps) {
  const environment = useContext(ToolEnvironmentContext)
  const store = environment.store
  const sessionID = childSessionId(messageId, call.id)
  const nests = store !== null && environment.renderTranscript !== null && depth < maxAgentDepth
  const known = useKnownChild(nests ? store : null, sessionID)
  const refused = useRefusedChild(nests ? store : null, sessionID)
  // Two things justify a watch. The gateway's own word that the child exists
  // — its created or updated session frame, a run it reports, or a bootstrap
  // that listed either — because a watch sent before then answers
  // unknown_session. Or the call's answer: a row reopened after a reload may
  // be a settled run whose run is pruned and whose session was never listed,
  // and the gateway can still resolve that persisted session. A finished tool
  // call is neither — the provider publishes it before the tool body runs,
  // and a dispatch creates the session in a goroutine. Once the gateway
  // refuses the child, the row waits for it to be announced, which the store
  // reports here as `refused`; that is what keeps a fetch that never ran from
  // retrying forever.
  const watching = nests && (known || (result !== undefined && !refused))
  // The cleanup runs on close, on unmount and on StrictMode's extra pass, so
  // the watch is dropped exactly once for each watch taken.
  useEffect(() => {
    if (!watching || store === null) {
      return
    }
    store.watchChild(sessionID)
    return () => store.unwatchChild(sessionID)
  }, [watching, store, sessionID])
  const messages = useChildMessages(nests ? store : null, sessionID)
  const error = useChildError(nests ? store : null, sessionID)
  const childStatus = useChildStatus(nests ? store : null, sessionID)
  // What the child block draws: its transcript once its snapshot lands, the
  // gateway's refusal if the subscription failed, and a loading note until
  // either arrives. A refusal is shown only while nothing loaded and no
  // answer is in hand: the store keeps just the last web_error, so an old one
  // must not outlive the transcript it failed to fetch, and an answer already
  // says what the tool did.
  let child: ReactNode = null
  if (nests) {
    if (messages !== undefined && messages.length > 0) {
      // The child's own run decides what its transcript draws; the parent's
      // status still counts while it runs, because a correlated snapshot may
      // not have landed yet.
      child = environment.renderTranscript?.(messages, depth + 1, isTurnActive(childStatus) || status === 'running')
    } else if (error !== null && (result === undefined || error.code !== unknownSessionCode)) {
      // Any refusal but a missing session is news: the session may exist and
      // only this connection or this read failed, so the row names it rather
      // than waiting. A missing session with an answer in hand is not news —
      // the answer is the story.
      child = (
        <p className={clsx(styles.note, styles.errorText)} role="alert">
          {`The subagent could not be loaded: ${error.error === '' ? error.code : error.error}`}
        </p>
      )
    } else if (watching) {
      child = <p className={styles.note}>Loading the subagent…</p>
    }
  }
  return (
    <>
      {child === null ? null : <div className={styles.nested}>{child}</div>}
      {result === undefined ? null : result.isError ? (
        <ToolSection label="Error">
          <OutputBlock text={result.content} tone="error" revealKey={`${call.id}:error`} />
        </ToolSection>
      ) : (
        <ToolSection label="Answer">
          <div className={styles.document}>
            <Markdown source={result.content} />
          </div>
        </ToolSection>
      )}
    </>
  )
}

export const agentView: ToolView = {
  title: () => 'Agent',
  summary: (input) => firstLine(stringField(input, 'prompt')),
  Body: AgentBody,
}

export const researchView: ToolView = {
  title: () => 'Research',
  summary: (input) => stringField(input, 'url') || firstLine(stringField(input, 'prompt')),
  Body: AgentBody,
}

export const dispatchView: ToolView = {
  title: () => 'Agent',
  summary: (input) => firstLine(stringField(input, 'prompt') || stringField(input, 'task')),
  Body: AgentBody,
}
