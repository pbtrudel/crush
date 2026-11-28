// ToolInput is a tool call's arguments. They arrive as a JSON string, and
// a call that is still streaming carries half of one, so every reader
// tolerates null and missing fields.
export type ToolInput = Record<string, unknown>

export function parseInput(raw: string): ToolInput | null {
  if (raw.trim() === '') {
    return null
  }
  try {
    const value: unknown = JSON.parse(raw)
    return typeof value === 'object' && value !== null && !Array.isArray(value) ? (value as ToolInput) : null
  } catch {
    return null
  }
}

export function stringField(input: ToolInput | null, key: string): string {
  const value = input?.[key]
  return typeof value === 'string' ? value : ''
}

export function numberField(input: ToolInput | null, key: string): number | null {
  const value = input?.[key]
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

export function boolField(input: ToolInput | null, key: string): boolean {
  return input?.[key] === true
}

export function arrayField(input: ToolInput | null, key: string): unknown[] {
  const value = input?.[key]
  return Array.isArray(value) ? value : []
}

// clipLine cuts a line to max columns, ellipsis included. It never keeps
// half a surrogate pair: that half renders as a replacement glyph.
function clipLine(line: string, max: number): string {
  if (line.length <= max) {
    return line
  }
  let end = max - 1
  const last = line.charCodeAt(end - 1)
  if (last >= 0xd800 && last <= 0xdbff) {
    end -= 1
  }
  return `${line.slice(0, end)}…`
}

export function firstLine(value: string, max = 80): string {
  // The line break of a CRLF pair stays on the first line after the split,
  // and a summary never shows it.
  const line = (value.trim().split('\n')[0] ?? '').replace(/\r$/, '')
  return clipLine(line, max)
}

// compactInput is the summary of a call no view knows: its first string
// argument, or the arguments as one line of JSON.
export function compactInput(input: ToolInput | null): string {
  if (input === null) {
    return ''
  }
  for (const value of Object.values(input)) {
    if (typeof value === 'string' && value !== '') {
      return firstLine(value)
    }
  }
  return firstLine(JSON.stringify(input))
}
