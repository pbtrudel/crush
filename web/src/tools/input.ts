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

export function firstLine(value: string, max = 80): string {
  const line = value.trim().split('\n')[0] ?? ''
  return line.length > max ? `${line.slice(0, max - 1)}…` : line
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
