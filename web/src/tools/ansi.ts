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

// sequence matches every escape a terminal consumes, in the order that
// keeps their bodies apart: a CSI (parameters, intermediates, a final
// byte), the same CSI in its 8-bit C1 form, an OSC (a title or a
// hyperlink), one of the other string sequences (DCS, SOS, PM, APC), and
// every other escape (intermediates and one final byte). Every string
// sequence is bound to one line, so a broken one costs a line instead of
// the rest of the output. Only an SGR CSI changes the style; the rest
// vanish, the 8-bit CSI among them: it is stripped whole rather than read
// as style, because nothing in a build log emits one as SGR.
const sequence =
  /\x1b\[([0-?]*)([ -/]*)([@-~])|\u009b[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b\n\u009c]*(?:\x07|\x1b\\|\u009c)?|\x1b[P_^X][^\x1b\n\u009c]*(?:\x1b\\|\u009c)?|\x1b(?!\[)[ -/]*[0-~]/g

// sgr is the parameter string of a real SGR. Anything else that ends in m
// (a keyboard report, a mode set) is an escape to strip, not a style.
const sgr = /^[0-9;:]*$/

// partialCsi is a CSI the output was cut inside of, at a line break or at
// the end of the text. The scanner needs its final byte, which never
// arrived, so the escape that opened it goes with the parameters it read.
const partialCsi = /\x1b\[[0-?]*[ -/]*(?=\n|$)/g

// controls removes the bytes no terminal draws: a bare escape the scanner
// could not pair with a sequence, and the C0 and C1 controls. A lone
// escape never swallows the digits after it. Tab stays; the line break and
// the carriage return are already split out.
const controls = /\x1b|[\x00-\x08\x0b\x0c\x0e-\x1f\x7f\u0080-\u009f]/g

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
  // Semicolons separate the parameters; a colon groups sub-parameters of
  // one parameter, and only its first value is read here.
  const groups = params === '' ? ['0'] : params.split(';')
  for (let index = 0; index < groups.length; index += 1) {
    const group = groups[index]
    const colon = group.indexOf(':')
    const code = Number(colon === -1 ? group : group.slice(0, colon)) || 0
    if (code === 38 || code === 48) {
      // Extended colour: 5;n or 2;r;g;b, or the colon form that carries its
      // own sub-parameters. Either way the colour is dropped and the
      // default colour, which reads on either theme, stays.
      if (colon !== -1) {
        continue
      }
      index += Number(groups[index + 1]) === 5 ? 2 : Number(groups[index + 1]) === 2 ? 4 : 0
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
      // 4:n turns the underline on for every n but zero.
      state.underline = colon === -1 || Number(group.slice(colon + 1)) !== 0
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

// parseAnsi turns terminal output into lines of styled spans.
export function parseAnsi(input: string): AnsiSpan[][] {
  const lines: AnsiSpan[][] = [[]]
  const state: SgrState = { bold: false, dim: false, italic: false, underline: false, fg: null, bg: null }
  // A return whose rewrite has not arrived yet. What the line already holds
  // stays visible until something is written after the return, and that
  // write starts at column 0. The flag survives the escapes a progress line
  // prints between its updates.
  let rewind = false
  const push = (chunk: string) => {
    const pieces = chunk.split('\n')
    pieces.forEach((piece, index) => {
      if (index > 0) {
        lines.push([])
        rewind = false
      }
      const line = lines[lines.length - 1]
      const segments = piece.includes('\r') ? piece.split('\r') : [piece]
      segments.forEach((segment, position) => {
        if (position > 0) {
          // A return only rewinds. It never clears the line by itself: a
          // progress line that ends in one keeps what it printed.
          rewind = true
        }
        if (segment === '') {
          return
        }
        if (rewind) {
          line.length = 0
          rewind = false
        }
        const text = segment.replace(controls, '')
        if (text !== '') {
          line.push({ text, classes: classesOf(state) })
        }
      })
    })
  }
  const source = input.replace(/\r\n/g, '\n').replace(partialCsi, '')
  let last = 0
  for (const match of source.matchAll(sequence)) {
    push(source.slice(last, match.index))
    last = (match.index ?? 0) + match[0].length
    // A plain parameter list with no intermediates is the only shape an
    // SGR takes; anything else that ends in m is an escape to strip.
    if (match[3] === 'm' && (match[2] ?? '') === '' && sgr.test(match[1] ?? '')) {
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
