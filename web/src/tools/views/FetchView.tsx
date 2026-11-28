import Markdown from '../../chat/markdown/Markdown'
import { stringField } from '../input'
import OutputBlock, { ToolSection } from '../OutputBlock'
import styles from '../Tools.module.css'
import type { ToolBodyProps, ToolView } from '../view'

// FetchBody shows what a page answered. Markdown and text go through the
// safe renderer, which shows raw HTML as text and keeps every link to an
// absolute web or mail address; an HTML answer is shown as its source.
// Markdown caps what it parses at 32 KiB and prints the rest as text, so a
// page cannot stall the log.
function FetchBody({ call, input, result }: ToolBodyProps) {
  if (result === undefined) {
    return <p className={styles.note}>Fetching…</p>
  }
  if (result.isError) {
    return (
      <ToolSection label="Error">
        <OutputBlock text={result.content} tone="error" revealKey={`${call.id}:error`} />
      </ToolSection>
    )
  }
  // The tool lowercases the format itself, so the view reads it the same
  // way: an answer in HTML is shown as the source the page sent.
  if (stringField(input, 'format').toLowerCase() === 'html') {
    return <OutputBlock text={result.content} revealKey={`${call.id}:source`} />
  }
  return (
    <div className={styles.document}>
      <Markdown source={result.content} />
    </div>
  )
}

export const fetchView: ToolView = {
  title: () => 'Fetch',
  summary: (input) => stringField(input, 'url'),
  Body: FetchBody,
}

export const webFetchView: ToolView = {
  title: () => 'Fetch',
  summary: (input) => stringField(input, 'url'),
  Body: FetchBody,
}

export const webSearchView: ToolView = {
  title: () => 'Search the web',
  summary: (input) => stringField(input, 'query'),
  Body: FetchBody,
}

// DownloadBody shows what the download answered, which is a line naming
// the file and its size.
function DownloadBody({ call, result }: ToolBodyProps) {
  if (result === undefined) {
    return <p className={styles.note}>Downloading…</p>
  }
  return (
    <OutputBlock
      text={result.content}
      tone={result.isError ? 'error' : 'plain'}
      revealKey={`${call.id}:output`}
    />
  )
}

export const downloadView: ToolView = {
  title: () => 'Download',
  summary: (input) => stringField(input, 'url'),
  Body: DownloadBody,
}
