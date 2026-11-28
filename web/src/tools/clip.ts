// The tool views share one line and character clip, so an output block, a
// terminal, a diff and a file view all cut text at the same points.

// maxOutputChars bounds what a collapsed tool view renders after the line
// clip. The line clip alone does not: a tool result can be one enormous
// line, and JSON escapes its newlines, so an argument can arrive as one too.
export const maxOutputChars = 20000

// splitLines counts a document's lines without letting a final newline add
// an empty one: a result of exactly maxLines lines and a newline is whole.
export function splitLines(text: string): string[] {
  return (text.endsWith('\n') ? text.slice(0, -1) : text).split('\n')
}

// clipChars cuts text to max UTF-16 code units. It drops a dangling high
// surrogate: half a pair renders as a replacement glyph.
export function clipChars(text: string, max: number): string {
  let end = max
  const last = text.charCodeAt(end - 1)
  if (last >= 0xd800 && last <= 0xdbff) {
    end -= 1
  }
  return text.slice(0, end)
}

// thousands names a character count in whole thousands for a reveal label.
export function thousands(length: number): number {
  return Math.ceil(length / 1000)
}

// ClippedText is one document cut to the line cap, then the character cap.
export interface ClippedText {
  text: string
  lineClipped: boolean
  charClipped: boolean
  // lines is the document's own line count, not the shown one.
  lines: number
}

// clipText cuts a document to maxLines lines, then maxChars characters. The
// line cap runs first: a long result of short lines is its first 40 lines,
// not the first 20000 characters of it, which would be hundreds of lines.
export function clipText(text: string, maxLines: number, maxChars = maxOutputChars): ClippedText {
  const lines = splitLines(text)
  const lineClipped = lines.length > maxLines
  const byLines = lineClipped ? lines.slice(0, maxLines).join('\n') : text
  const charClipped = byLines.length > maxChars
  return {
    text: charClipped ? clipChars(byLines, maxChars) : byLines,
    lineClipped,
    charClipped,
    lines: lines.length,
  }
}
