import { useMemo } from 'react'

import CodeBlock from '../../chat/markdown/CodeBlock'
import { languageForPath } from '../../chat/markdown/languages'
import { diffFromPatch, diffStats, diffVersions, type DiffLine } from '../diff'
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

// editPairs is the call's own strings: one pair per edit of a multiedit,
// else the single old and new string.
function editPairs(input: ToolInput | null): [string, string][] {
  const edits = editList(input)
  if (edits.length > 0) {
    return edits.map((edit) => [stringField(edit, 'old_string'), stringField(edit, 'new_string')])
  }
  return [[stringField(input, 'old_string'), stringField(input, 'new_string')]]
}

// lastNewString is the text the call wrote, for the fallback when no diff
// of it can be drawn.
function lastNewString(input: ToolInput | null): string {
  const edits = editList(input)
  const last = edits.at(-1)
  return last === undefined ? stringField(input, 'new_string') : stringField(last, 'new_string')
}

interface Diffed {
  lines: DiffLine[]
  // gaveUp is true when neither the file's two versions nor the call's own
  // strings could be diffed, so the view has to show the text instead.
  gaveUp: boolean
}

// editDiff diffs the file's two versions, falls back to the call's own
// strings when that pair is too far apart, and gives up only when both
// are. Every attempt pays the same bounds.
function editDiff(filePair: [string, string] | null, callPairs: [string, string][]): Diffed {
  if (filePair !== null) {
    const file = diffVersions(filePair[0], filePair[1])
    if (!file.tooLarge) {
      return { lines: file.lines, gaveUp: false }
    }
  }
  const answers = callPairs.map(([old, next]) => diffVersions(old, next))
  if (answers.length > 0 && answers.every((answer) => !answer.tooLarge)) {
    return { lines: answers.flatMap((answer) => answer.lines), gaveUp: false }
  }
  return { lines: [], gaveUp: true }
}

function EditBody({ call, input, result }: ToolBodyProps) {
  const isError = result?.isError === true
  const metadata = isError ? null : parseInput(result?.metadata ?? '')
  const before = stringField(metadata, 'old_content')
  const after = stringField(metadata, 'new_content')
  const finished = call.finished
  const filePair = useMemo<[string, string] | null>(
    () => (before !== '' || after !== '' ? [before, after] : null),
    [before, after],
  )
  // A failed call has no change to diff, and half-written arguments are
  // not worth one: the row reads that it is still being written instead.
  const callPairs = useMemo(
    () => (finished && !isError ? editPairs(input) : []),
    [finished, isError, input],
  )
  const diffed = useMemo(() => editDiff(filePair, callPairs), [filePair, callPairs])
  const newText = after !== '' ? after : lastNewString(input)
  if (isError) {
    return <ErrorSection content={result?.content ?? ''} revealKey={`${call.id}:error`} />
  }
  if (filePair === null && callPairs.length === 0) {
    return <p className={styles.note}>Editing…</p>
  }
  if (diffed.gaveUp) {
    return (
      <>
        <p className={styles.note}>Diff too large: showing the new text instead.</p>
        {newText === '' ? null : (
          <ToolSection label="New content">
            <OutputBlock text={newText} revealKey={`${call.id}:new`} />
          </ToolSection>
        )}
      </>
    )
  }
  return <DiffView lines={diffed.lines} revealKey={`${call.id}:diff`} />
}

function editSummary(input: ToolInput | null, result: ToolBodyProps['result']): string {
  const path = stringField(input, 'file_path')
  // A failed edit changed nothing, so it names no counts of its own.
  return result?.isError ? path : `${path}${counts(parseInput(result?.metadata ?? ''))}`
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
