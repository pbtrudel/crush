import ExitCode from '../ExitCode'
import { boolField, firstLine, numberField, parseInput, stringField } from '../input'
import OutputBlock, { ToolSection } from '../OutputBlock'
import Terminal from '../Terminal'
import styles from '../Tools.module.css'
import type { ToolBodyProps, ToolView } from '../view'

function BashBody({ call, input, result }: ToolBodyProps) {
  const metadata = parseInput(result?.metadata ?? '')
  const command = stringField(input, 'command')
  // The metadata keeps the raw output with its escapes; the result text is
  // what the model read, with the working directory appended.
  const output = stringField(metadata, 'output') || (result?.content ?? '')
  const exit = numberField(metadata, 'exit_code')
  const shellId = stringField(metadata, 'shell_id')
  return (
    <>
      <Terminal
        command={command}
        output={result?.isError ? '' : output}
        revealKey={`${call.id}:terminal`}
      />
      {result?.isError ? (
        <ToolSection label="Error">
          <OutputBlock text={result.content} tone="error" revealKey={`${call.id}:error`} />
        </ToolSection>
      ) : null}
      {/* A job still running has no exit code; once the command has one it
          has finished and the note would be stale. */}
      {boolField(metadata, 'background') && shellId !== '' && exit === null ? (
        <p className={styles.note}>Running in the background as job {shellId}</p>
      ) : null}
      {exit !== null ? <ExitCode code={exit} /> : null}
    </>
  )
}

export const bashView: ToolView = {
  title: () => 'Bash',
  summary: (input) => firstLine(stringField(input, 'description') || stringField(input, 'command')),
  Body: BashBody,
}

function JobBody({ call, result }: ToolBodyProps) {
  const metadata = parseInput(result?.metadata ?? '')
  return (
    <Terminal
      command={stringField(metadata, 'command')}
      output={result?.content ?? ''}
      revealKey={`${call.id}:job`}
    />
  )
}

export const jobOutputView: ToolView = {
  title: () => 'Job output',
  summary: (input) => stringField(input, 'shell_id'),
  Body: JobBody,
}

export const jobKillView: ToolView = {
  title: () => 'Stop job',
  summary: (input) => stringField(input, 'shell_id'),
  Body: JobBody,
}
