import { type ReactNode } from 'react'

import CopyButton from '../CopyButton'
import type { HighlightedToken } from './highlighter'
import { useHighlight } from './useHighlight'
import styles from './CodeBlock.module.css'

export interface CodeBlockProps {
  code: string
  language?: string | null
  // lineStart numbers the first line; lineNumbers turns the gutter on.
  lineStart?: number
  lineNumbers?: boolean
}

function tokens(line: HighlightedToken[]): ReactNode[] {
  return line.map((token, index) =>
    token.className === '' ? token.text : (
      <span key={index} className={token.className}>
        {token.text}
      </span>
    ),
  )
}

// CodeBlock is a fenced block or a file excerpt. It renders plain text at
// once and swaps in the highlighted lines when the lazy Shiki chunk
// answers; an unknown language stays plain.
export default function CodeBlock({ code, language = null, lineStart = 1, lineNumbers = false }: CodeBlockProps) {
  const highlight = useHighlight(code, language)
  const rows: ReactNode[][] =
    highlight === null
      ? code.split('\n').map((line) => [line])
      : [
          ...highlight.lines.map(tokens),
          ...(highlight.covered < code.length ? code.slice(highlight.covered).split('\n').map((line) => [line]) : []),
        ]
  return (
    <div className={styles.block}>
      <div className={styles.header}>
        <span className={styles.label}>{language ?? ''}</span>
        <CopyButton text={code} label="Copy code" />
      </div>
      <pre className={styles.pre}>
        <code>
          {rows.map((row, index) => (
            <span key={index} className={styles.line}>
              {lineNumbers ? (
                <span className={styles.gutter} aria-hidden="true">
                  {lineStart + index}
                </span>
              ) : null}
              {row}
            </span>
          ))}
        </code>
      </pre>
    </div>
  )
}
