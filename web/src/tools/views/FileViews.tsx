import { useMemo } from 'react'

import CodeBlock from '../../chat/markdown/CodeBlock'
import { languageForPath } from '../../chat/markdown/languages'
import { boundDiffInput, diffFromPatch, diffStats, diffVersions, maxDiffInputChars } from '../diff'
import DiffView from '../DiffView'
import { arrayField, numberField, parseInput, stringField, type ToolInput } from '../input'
import OutputBlock, { ToolSection } from '../OutputBlock'
import styles from '../Tools.module.css'
import type { ToolBodyProps, ToolView } from '../view'

function ErrorSection({ content, revealKey }: { content: string; revealKey: string }) {
  return (
    <ToolSection label="Error">
      <OutputBlock text={content} tone="error" revealKey={revealKey} />
    </ToolSection>
  )
}

function counts(metadata: ToolInput | null): string {
  const added = numberField(metadata, 'additions')
  const removed = numberField(metadata, 'removals')
  return added === null || removed === null ? '' : ` +${added} −${removed}`
}

function ReadBody({ call, input, result }: ToolBodyProps) {
  if (result === undefined) {
    return <p className={styles.note}>Reading…</p>
  }
  if (result.isError) {
    return <ErrorSection content={result.content} revealKey={`${call.id}:error`} />
  }
  const metadata = parseInput(result.metadata)
  const content = stringField(metadata, 'content')
  // A read of several files carries no single slice: the text is the view.
  if (content === '') {
    return <OutputBlock text={result.content} revealKey={`${call.id}:read`} />
  }
  const path = stringField(metadata, 'file_path') || stringField(input, 'file_path')
  return (
    <CodeBlock
      code={content}
      language={languageForPath(path)}
      lineNumbers
      lineStart={(numberField(input, 'offset') ?? 0) + 1}
    />
  )
}

export const readView: ToolView = {
  title: () => 'Read',
  summary: (input) => {
    const paths = arrayField(input, 'file_paths').filter((entry): entry is string => typeof entry === 'string')
    return paths.length > 0 ? `${paths.length} files` : stringField(input, 'file_path')
  },
  Body: ReadBody,
}

// editList is a multiedit's edits. Some models send the array as a JSON
// string holding one, which the tool decodes too, so the view reads both.
function editList(input: ToolInput | null): ToolInput[] {
  const raw = input?.['edits']
  let entries: unknown[] = []
  if (Array.isArray(raw)) {
    entries = raw
  } else if (typeof raw === 'string') {
    try {
      const parsed: unknown = JSON.parse(raw)
      entries = Array.isArray(parsed) ? parsed : []
    } catch {
      entries = []
    }
  }
  const out: ToolInput[] = []
  for (const entry of entries) {
    if (typeof entry === 'object' && entry !== null && !Array.isArray(entry)) {
      out.push(entry as ToolInput)
    }
  }
  return out
}

// editPairs is what the call changed: the file's whole before and after
// when the tool recorded them, else the pairs the call itself carries,
// one per edit of a multiedit. While the arguments stream there is no
// pair: half-written strings cost a diff each delta and say nothing.
function editPairs(
  finished: boolean,
  input: ToolInput | null,
  before: string,
  after: string,
): [string, string][] {
  if (before !== '' || after !== '') {
    return [[before, after]]
  }
  if (!finished) {
    return []
  }
  const edits = editList(input)
  if (edits.length > 0) {
    return edits.map((edit) => [stringField(edit, 'old_string'), stringField(edit, 'new_string')])
  }
  return [[stringField(input, 'old_string'), stringField(input, 'new_string')]]
}

const capChars = maxDiffInputChars.toLocaleString('en-US')

function EditBody({ call, input, result }: ToolBodyProps) {
  const metadata = result?.isError ? null : parseInput(result?.metadata ?? '')
  const before = stringField(metadata, 'old_content')
  const after = stringField(metadata, 'new_content')
  const finished = call.finished
  const pairs = useMemo(() => editPairs(finished, input, before, after), [finished, input, before, after])
  // The versions are capped before jsdiff runs, so a huge file cannot
  // stall the row; the note below says when a cap applied.
  const bounded = useMemo(() => pairs.map(([old, next]) => boundDiffInput(old, next)), [pairs])
  const diffed = useMemo(
    () => bounded.map((entry) => diffVersions(entry.before, entry.after)),
    [bounded],
  )
  if (result?.isError) {
    return <ErrorSection content={result.content} revealKey={`${call.id}:error`} />
  }
  if (pairs.length === 0) {
    return <p className={styles.note}>Editing…</p>
  }
  const tooLarge = diffed.some((entry) => entry.tooLarge)
  const lines = diffed.flatMap((entry) => entry.lines)
  return (
    <>
      {tooLarge || bounded.some((entry) => entry.clipped) ? (
        <p className={styles.note}>
          {tooLarge
            ? 'Diff too large: this edit changes more lines than the view will diff.'
            : `Diff too large: showing the first ${capChars} characters of each version.`}
        </p>
      ) : null}
      {tooLarge ? null : <DiffView lines={lines} revealKey={`${call.id}:diff`} />}
    </>
  )
}

function editSummary(input: ToolInput | null, result: ToolBodyProps['result']): string {
  return `${stringField(input, 'file_path')}${counts(parseInput(result?.metadata ?? ''))}`
}

export const editView: ToolView = { title: () => 'Edit', summary: editSummary, Body: EditBody }
export const multiEditView: ToolView = { title: () => 'Edit', summary: editSummary, Body: EditBody }

function WriteBody({ call, input, result }: ToolBodyProps) {
  const patch = stringField(parseInput(result?.metadata ?? ''), 'diff')
  const lines = useMemo(() => diffFromPatch(patch), [patch])
  if (result?.isError) {
    return <ErrorSection content={result.content} revealKey={`${call.id}:error`} />
  }
  if (lines.length > 0) {
    return <DiffView lines={lines} revealKey={`${call.id}:diff`} />
  }
  const path = stringField(input, 'file_path')
  return <CodeBlock code={stringField(input, 'content')} language={languageForPath(path)} lineNumbers />
}

export const writeView: ToolView = {
  title: () => 'Write',
  summary: (input, result) => {
    const lines = diffFromPatch(stringField(parseInput(result?.metadata ?? ''), 'diff'))
    const stats = diffStats(lines)
    return `${stringField(input, 'file_path')}${lines.length > 0 ? ` +${stats.added} −${stats.removed}` : ''}`
  },
  Body: WriteBody,
}
