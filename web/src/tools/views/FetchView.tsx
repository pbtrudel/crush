import Markdown from '../../chat/markdown/Markdown'
import { stringField } from '../input'
import OutputBlock, { ToolSection } from '../OutputBlock'
import styles from '../Tools.module.css'
import type { ToolBodyProps, ToolView } from '../view'

// unwrapMarkdown removes the fence the fetch tool puts around a page it
// converted: for format markdown it answers "```\n" + content + "\n```"
// (internal/agent/tools/fetch.go). Only that first line and one trailing
// fence go, so a fenced block the page wrote itself stays. A page cut at the
// size limit can lose the closing fence, so it is optional.
function unwrapMarkdown(content: string): string {
  if (!content.startsWith('```\n')) {
    return content
  }
  const body = content.slice('```\n'.length)
  return body.endsWith('\n```') ? body.slice(0, -'\n```'.length) : body
}

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
  const format = stringField(input, 'format').toLowerCase()
  if (format === 'html') {
    return <OutputBlock text={result.content} revealKey={`${call.id}:source`} />
  }
  return (
    <div className={styles.document}>
      <Markdown source={format === 'markdown' ? unwrapMarkdown(result.content) : result.content} />
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
