export interface AnsiSpan {
  text: string
  classes: readonly string[]
}

const colors = ['black', 'red', 'green', 'yellow', 'blue', 'magenta', 'cyan', 'white']

interface SgrState {
  bold: boolean
  dim: boolean
  italic: boolean
  underline: boolean
  fg: string | null
  bg: string | null
}

// sequence matches every escape a terminal consumes: CSI (colours, cursor
// moves, erases), OSC (titles, hyperlinks) ended by BEL or ST, and the
// charset designations (ESC ( B) and the other two-byte escapes. Only
// CSI ... m changes the style; the rest vanish.
const sequence = /\x1b\[([0-9;:?]*)([@-~])|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)?|\x1b[()*+][0-9A-Za-z]|\x1b[@-Z\\-_]/g

// controls removes the bytes no terminal draws. A sequence the regex did
// not complete is cut at the escape: output clipped mid-escape must not
// leave its parameters in the text. Tab stays; the line break and the
// carriage return are already split out.
const controls = /\x1b\[?[0-9;:?]*|[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/g

function classesOf(state: SgrState): string[] {
  const out: string[] = []
  if (state.bold) {
    out.push('bold')
  }
  if (state.dim) {
    out.push('dim')
  }
  if (state.italic) {
    out.push('italic')
  }
  if (state.underline) {
    out.push('underline')
  }
  if (state.fg !== null) {
    out.push(`fg-${state.fg}`)
  }
  if (state.bg !== null) {
    out.push(`bg-${state.bg}`)
  }
  return out
}

function reset(state: SgrState) {
  state.bold = false
  state.dim = false
  state.italic = false
  state.underline = false
  state.fg = null
  state.bg = null
}

function applySgr(state: SgrState, params: string) {
  const codes = params === '' ? [0] : params.split(/[;:]/).map((code) => Number(code) || 0)
  for (let index = 0; index < codes.length; index += 1) {
    const code = codes[index]
    if (code === 38 || code === 48) {
      // Extended colour: 5;n or 2;r;g;b. The parameters are consumed and
      // the colour dropped; the default colour reads on either theme.
      index += codes[index + 1] === 5 ? 2 : codes[index + 1] === 2 ? 4 : 0
      continue
    }
    if (code === 0) {
      reset(state)
    } else if (code === 1) {
      state.bold = true
    } else if (code === 2) {
      state.dim = true
    } else if (code === 3) {
      state.italic = true
    } else if (code === 4) {
      state.underline = true
    } else if (code === 22) {
      state.bold = false
      state.dim = false
    } else if (code === 23) {
      state.italic = false
    } else if (code === 24) {
      state.underline = false
    } else if (code >= 30 && code <= 37) {
      state.fg = colors[code - 30]
    } else if (code === 39) {
      state.fg = null
    } else if (code >= 40 && code <= 47) {
      state.bg = colors[code - 40]
    } else if (code === 49) {
      state.bg = null
    } else if (code >= 90 && code <= 97) {
      state.fg = `bright-${colors[code - 90]}`
    } else if (code >= 100 && code <= 107) {
      state.bg = `bright-${colors[code - 100]}`
    }
  }
}

// lastSegment is the last carriage-return segment of a line that holds
// text. A terminal rewinds to column 0 at a bare return, but what it wrote
// before stays on the screen until something overwrites it, so a progress
// line that ends in a return keeps its last write.
function lastSegment(piece: string): string {
  if (!piece.includes('\r')) {
    return piece
  }
  const segments = piece.split('\r')
  for (let index = segments.length - 1; index >= 0; index -= 1) {
    if (segments[index] !== '') {
      return segments[index]
    }
  }
  return ''
}

// parseAnsi turns terminal output into lines of styled spans.
export function parseAnsi(input: string): AnsiSpan[][] {
  const lines: AnsiSpan[][] = [[]]
  const state: SgrState = { bold: false, dim: false, italic: false, underline: false, fg: null, bg: null }
  // A return whose rewrite has not arrived yet. The text before it stays
  // visible, and the first text after it replaces the line, even across
  // the escapes a progress line prints between its updates.
  let rewind = false
  const push = (chunk: string) => {
    const pieces = chunk.split('\n')
    pieces.forEach((piece, index) => {
      if (index > 0) {
        lines.push([])
        rewind = false
      }
      if (piece === '') {
        return
      }
      const line = lines[lines.length - 1]
      const returns = piece.includes('\r')
      if (rewind || returns) {
        line.length = 0
        rewind = false
      }
      const text = lastSegment(piece).replace(controls, '')
      if (text !== '') {
        line.push({ text, classes: classesOf(state) })
      }
      if (piece.endsWith('\r')) {
        rewind = true
      }
    })
  }
  const source = input.replace(/\r\n/g, '\n')
  let last = 0
  for (const match of source.matchAll(sequence)) {
    push(source.slice(last, match.index))
    last = (match.index ?? 0) + match[0].length
    if (match[2] === 'm') {
      applySgr(state, match[1] ?? '')
    }
  }
  push(source.slice(last))
  // Adjacent spans with the same classes are one run.
  return lines.map((line) =>
    line.reduce<AnsiSpan[]>((merged, span) => {
      const previous = merged[merged.length - 1]
      if (previous !== undefined && previous.classes.join(' ') === span.classes.join(' ')) {
        merged[merged.length - 1] = { text: previous.text + span.text, classes: previous.classes }
      } else {
        merged.push(span)
      }
      return merged
    }, []),
  )
}
