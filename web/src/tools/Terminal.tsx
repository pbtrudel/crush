import { clsx } from 'clsx'
import { useCallback, useContext, useMemo, useState, useSyncExternalStore } from 'react'

import { ToolEnvironmentContext } from './environment'
import { maxOutputChars } from './OutputBlock'
import { parseAnsi } from './ansi'
import styles from './Terminal.module.css'

export interface TerminalProps {
  command?: string
  output: string
  maxLines?: number
  // revealKey names the revealed state in the environment, the same way an
  // OutputBlock does, so "Show all" survives the log remounting the row.
  revealKey?: string
}

interface Clipped {
  text: string
  lines: number
  clipped: boolean
  charClipped: boolean
}

// clipChars cuts text to max UTF-16 code units, dropping a dangling high
// surrogate: half a pair renders as a replacement glyph.
function clipChars(text: string, max: number): string {
  let end = max
  const last = text.charCodeAt(end - 1)
  if (last >= 0xd800 && last <= 0xdbff) {
    end -= 1
  }
  return text.slice(0, end)
}

// countLines counts a document's lines without letting a final newline add
// an empty one.
function countLines(text: string): number {
  const end = text.endsWith('\n') ? text.length - 1 : text.length
  let lines = 1
  for (let index = 0; index < end; index += 1) {
    if (text.charCodeAt(index) === 10) {
      lines += 1
    }
  }
  return lines
}

// clipLines cuts output to maxLines lines and maxOutputChars characters
// before it is parsed. Parsing is a regular expression over the whole
// string and a tool result can be megabytes, so the scan stops at the
// limit: only the text a reader can see is parsed, and the reveal parses
// the rest once.
function clipLines(text: string, maxLines: number): Clipped {
  let end = text.length
  let shown = 1
  for (let index = 0; index < text.length; index += 1) {
    if (text.charCodeAt(index) === 10) {
      shown += 1
      if (shown > maxLines) {
        end = index
        break
      }
    }
  }
  const lineClipped = shown > maxLines
  let visible = text.slice(0, end)
  let charClipped = false
  if (visible.length > maxOutputChars) {
    visible = clipChars(visible, maxOutputChars)
    charClipped = true
  }
  const clipped = lineClipped || charClipped
  return { text: visible, lines: clipped ? countLines(text) : shown, clipped, charClipped }
}

// Terminal is a command and its output, coloured from the ANSI escapes.
// The escapes become classes only: the CSP forbids inline style, and no
// control sequence ever reaches the DOM.
export default function Terminal({ command = '', output, maxLines = 200, revealKey }: TerminalProps) {
  const environment = useContext(ToolEnvironmentContext)
  const [localAll, setLocalAll] = useState(false)
  const readRevealed = useCallback(
    () => revealKey !== undefined && environment.revealed.has(revealKey),
    [environment, revealKey],
  )
  const revealed = useSyncExternalStore(environment.revealed.subscribe, readRevealed)
  const all = revealKey === undefined ? localAll : revealed
  // The clip is memoised by the output, and the parse by the clipped text,
  // so a re-render costs neither scan nor regex.
  const shown = useMemo(
    () =>
      all
        ? { text: output, lines: 0, clipped: false, charClipped: false }
        : clipLines(output, maxLines),
    [all, output, maxLines],
  )
  const lines = useMemo(() => parseAnsi(shown.text), [shown.text])
  // The label names the limit that cut the text. The character cap runs
  // last, so where both apply it is the one to say.
  const showAll = shown.charClipped
    ? `Show all (${Math.ceil(output.length / 1000)} K characters)`
    : `Show all ${shown.lines} lines`
  return (
    <div className={styles.terminal}>
      {command !== '' ? (
        <div className={styles.command}>
          <span className={styles.prompt} aria-hidden="true">
            $
          </span>
          <span>{command}</span>
        </div>
      ) : null}
      {output !== '' ? (
        <pre className={styles.output}>
          {lines.map((line, index) => (
            <span key={index} className={styles.line}>
              {line.map((span, position) =>
                span.classes.length === 0 ? (
                  span.text
                ) : (
                  <span key={position} className={clsx(span.classes.map((name) => styles[name]))}>
                    {span.text}
                  </span>
                ),
              )}
            </span>
          ))}
        </pre>
      ) : null}
      {shown.clipped ? (
        <button
          type="button"
          className={styles.more}
          onClick={() => {
            if (revealKey === undefined) {
              setLocalAll(true)
            } else {
              environment.reveal(revealKey)
            }
          }}
        >
          {showAll}
        </button>
      ) : null}
    </div>
  )
}
