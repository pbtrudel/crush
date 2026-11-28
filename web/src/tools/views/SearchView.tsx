import { boolField, numberField, parseInput, stringField, type ToolInput } from '../input'
import OutputBlock, { ToolSection } from '../OutputBlock'
import styles from '../Tools.module.css'
import type { ToolBodyProps, ToolView } from '../view'

function SearchBody({ call, result }: ToolBodyProps) {
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
      {boolField(parseInput(result.metadata), 'truncated') ? (
        <p className={styles.note}>The list was cut short.</p>
      ) : null}
    </>
  )
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
  summary: (input, result) =>
    counted(where(input, stringField(input, 'pattern')), result, 'number_of_matches', 'match', 'matches'),
  Body: SearchBody,
}

export const globView: ToolView = {
  title: () => 'Glob',
  summary: (input, result) =>
    counted(where(input, stringField(input, 'pattern')), result, 'number_of_files', 'file', 'files'),
  Body: SearchBody,
}

export const lsView: ToolView = {
  title: () => 'List',
  summary: (input, result) =>
    counted(stringField(input, 'path') || '.', result, 'number_of_files', 'file', 'files'),
  Body: SearchBody,
}

export const sourcegraphView: ToolView = {
  title: () => 'Sourcegraph',
  summary: (input) => stringField(input, 'query'),
  Body: SearchBody,
}
