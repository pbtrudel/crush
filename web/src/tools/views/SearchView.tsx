import { boolField, numberField, parseInput, stringField, type ToolInput } from '../input'
import OutputBlock, { ToolSection } from '../OutputBlock'
import styles from '../Tools.module.css'
import type { ToolBodyProps, ToolView } from '../view'

// grepCutNote is how the grep tool's body reports its own truncation
// (internal/agent/tools/grep.go). Where it is present the view adds no
// note of its own.
const grepCutNote = '(Results are truncated.'

// SearchBody shows a search's output. showViewNote says whether the view
// may add "The list was cut short.": a body that already says it was cut
// needs no second notice.
function SearchBody({ call, result, showViewNote }: ToolBodyProps & { showViewNote: boolean }) {
  if (result === undefined) {
    return <p className={styles.note}>Searching…</p>
  }
  if (result.isError) {
    return (
      <ToolSection label="Error">
        <OutputBlock text={result.content} tone="error" revealKey={`${call.id}:error`} />
      </ToolSection>
    )
  }
  return (
    <>
      <OutputBlock text={result.content} revealKey={`${call.id}:output`} />
      {showViewNote && boolField(parseInput(result.metadata), 'truncated') ? (
        <p className={styles.note}>The list was cut short.</p>
      ) : null}
    </>
  )
}

function PlainSearchBody(props: ToolBodyProps) {
  return <SearchBody {...props} showViewNote />
}

// GrepBody leaves the truncation note to the tool: a cut grep body already
// ends with its own note.
function GrepBody(props: ToolBodyProps) {
  return <SearchBody {...props} showViewNote={!(props.result?.content ?? '').includes(grepCutNote)} />
}

function plural(count: number, one: string, many: string): string {
  return `${count} ${count === 1 ? one : many}`
}

function where(input: ToolInput | null, subject: string): string {
  const path = stringField(input, 'path')
  return path === '' ? subject : `${subject} in ${path}`
}

function counted(
  summary: string,
  result: ToolBodyProps['result'],
  key: string,
  one: string,
  many: string,
): string {
  const count = numberField(parseInput(result?.metadata ?? ''), key)
  return count === null ? summary : `${summary} · ${plural(count, one, many)}`
}

export const grepView: ToolView = {
  title: () => 'Grep',
  summary: (input, result) => {
    const metadata = parseInput(result?.metadata ?? '')
    const subject = where(input, stringField(input, 'pattern'))
    const count = numberField(metadata, 'number_of_matches')
    if (count === null) {
      return subject
    }
    // A cut search stopped at its cap, so its count is a floor and must not
    // read as a total.
    return `${subject} · ${boolField(metadata, 'truncated') ? `${count}+ matches` : plural(count, 'match', 'matches')}`
  },
  Body: GrepBody,
}

export const globView: ToolView = {
  title: () => 'Glob',
  summary: (input, result) =>
    counted(where(input, stringField(input, 'pattern')), result, 'number_of_files', 'file', 'files'),
  Body: PlainSearchBody,
}

export const lsView: ToolView = {
  title: () => 'List',
  summary: (input, result) =>
    counted(stringField(input, 'path') || '.', result, 'number_of_files', 'file', 'files'),
  Body: PlainSearchBody,
}

export const sourcegraphView: ToolView = {
  title: () => 'Sourcegraph',
  summary: (input) => stringField(input, 'query'),
  Body: PlainSearchBody,
}
