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
  // The gateway's own word that the child exists is the only sign a watch can
  // rest on: a watch sent before then answers unknown_session and is never
  // retried, so it would be lost. That word is the child's created or updated
  // session frame, a run the gateway reports, or a bootstrap that listed
  // either. A finished tool call is not a sign — the provider publishes it
  // before the tool body runs, and a dispatch creates the session in a
  // goroutine — and neither is an answer, because a tool that never got to
  // run (a refused or blocked fetch) makes no session at all. The watch is
  // taken as soon as the child is known, even after its answer arrived.
  const watching = nests && known
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
    } else if (error !== null && result === undefined) {
      // A refusal the child's row can name: there is no answer to show
      // instead. With one in hand the answer is the story.
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
