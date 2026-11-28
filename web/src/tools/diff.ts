import { parsePatch, structuredPatch, type StructuredPatchHunk } from 'diff'

import { maxOutputChars } from './OutputBlock'

// DiffLine is one row of a unified diff: the hunk header, an added line, a
// removed line, or a line both versions share. A side that has no such
// line carries null, so the gutter shows nothing rather than a wrong
// number.
export interface DiffLine {
  kind: 'hunk' | 'add' | 'del' | 'context'
  text: string
  oldNumber: number | null
  newNumber: number | null
}

// maxDiffInputChars bounds each side of a diff. jsdiff's line pass is
// quadratic in the worst case, and a large file's metadata carries the
// whole file before and after: past this cap the view diffs the first
// 20000 characters of each version and says that it did.
export const maxDiffInputChars = maxOutputChars

// maxDiffEditLength bounds how different the two versions may be, in
// changed lines, before jsdiff gives up and answers undefined. The
// character cap alone does not bound the work: two 20000-character files
// that share no line take 22.6 s to diff unbounded, because Myers's pass
// costs the line count times the edit distance. Measured on this machine:
// 14 ms at 200, 28 ms at 500, 98 ms at 1000, 1.5 s at 4000; a 13000
// character file whose 200 lines were all rewritten is 402 apart and
// diffs in 10 ms. A thousand keeps a whole-file rewrite readable while no
// row can block the thread for more than about a tenth of a second.
export const maxDiffEditLength = 1000

// maxDiffLines is the line clip a diff shares with an output block: a diff
// longer than this is drawn down to it until the reader asks for the rest.
export const maxDiffLines = 40

// clipChars cuts text to max UTF-16 code units and drops a dangling high
// surrogate: half a pair renders as a replacement glyph. It matches the
// clip OutputBlock applies to a tool result.
function clipChars(text: string, max: number): string {
  let end = max
  const last = text.charCodeAt(end - 1)
  if (last >= 0xd800 && last <= 0xdbff) {
    end -= 1
  }
  return text.slice(0, end)
}

// boundDiffInput caps both versions at maxDiffInputChars, so no caller can
// hand jsdiff an unbounded string. clipped says a side was cut, and the
// view then names the cap instead of passing the diff off as the whole
// story.
export function boundDiffInput(
  before: string,
  after: string,
): { before: string; after: string; clipped: boolean } {
  const clipped = before.length > maxDiffInputChars || after.length > maxDiffInputChars
  return {
    before: before.length > maxDiffInputChars ? clipChars(before, maxDiffInputChars) : before,
    after: after.length > maxDiffInputChars ? clipChars(after, maxDiffInputChars) : after,
    clipped,
  }
}

// hunkLines numbers the lines of jsdiff's hunks on both sides.
function hunkLines(hunks: StructuredPatchHunk[]): DiffLine[] {
  const out: DiffLine[] = []
  for (const hunk of hunks) {
    out.push({
      kind: 'hunk',
      text: `@@ -${hunk.oldStart},${hunk.oldLines} +${hunk.newStart},${hunk.newLines} @@`,
      oldNumber: null,
      newNumber: null,
    })
    let oldNumber = hunk.oldStart
    let newNumber = hunk.newStart
    for (const line of hunk.lines) {
      const mark = line[0]
      const text = line.slice(1)
      if (mark === '\\') {
        // "\ No newline at end of file" describes the line above.
        continue
      }
      if (mark === '+') {
        out.push({ kind: 'add', text, oldNumber: null, newNumber })
        newNumber += 1
      } else if (mark === '-') {
        out.push({ kind: 'del', text, oldNumber, newNumber: null })
        oldNumber += 1
      } else {
        out.push({ kind: 'context', text, oldNumber, newNumber })
        oldNumber += 1
        newNumber += 1
      }
    }
  }
  return out
}

// DiffVersions is a diff of two versions of one file, and whether jsdiff
// gave up on it: a version pair this far apart is not drawn at all.
export interface DiffVersions {
  lines: DiffLine[]
  tooLarge: boolean
}

// lastDiff is one computed diff. The virtualized log unmounts a row that
// scrolls away and mounts it again, and the second mount asks for the same
// two strings; holding the last answer keeps that from running jsdiff
// again. Comparing two strings is a memcmp, and there is one slot, so the
// memory a diff can pin is bounded by the input cap.
let lastDiff: { before: string; after: string; context: number; answer: DiffVersions } | null = null

// diffVersions diffs two versions of a file in the tab, within both the
// character cap and the edit-distance cap. tooLarge is true when jsdiff
// gave up: the view then says so instead of drawing a wrong diff.
export function diffVersions(before: string, after: string, context = 3): DiffVersions {
  if (
    lastDiff !== null &&
    lastDiff.before === before &&
    lastDiff.after === after &&
    lastDiff.context === context
  ) {
    return lastDiff.answer
  }
  const bounded = boundDiffInput(before, after)
  const patch = structuredPatch('a', 'b', bounded.before, bounded.after, '', '', {
    context,
    maxEditLength: maxDiffEditLength,
  })
  const answer: DiffVersions =
    patch === undefined ? { lines: [], tooLarge: true } : { lines: hunkLines(patch.hunks), tooLarge: false }
  lastDiff = { before, after, context, answer }
  return answer
}

// diffContents diffs two versions of a file in the tab. Both sides are
// capped at maxDiffInputChars first; use boundDiffInput to learn whether a
// side was cut, and diffVersions to learn whether jsdiff gave up.
export function diffContents(before: string, after: string, context = 3): DiffLine[] {
  return diffVersions(before, after, context).lines
}

// diffFromPatch reads a unified diff, such as the write tool's metadata.
// Text that is not a patch answers no lines: the view then falls back to
// the content the call carries.
export function diffFromPatch(unified: string): DiffLine[] {
  if (unified.trim() === '') {
    return []
  }
  try {
    return parsePatch(unified).flatMap((file) => hunkLines(file.hunks))
  } catch {
    return []
  }
}

export function diffStats(lines: DiffLine[]): { added: number; removed: number } {
  let added = 0
  let removed = 0
  for (const line of lines) {
    if (line.kind === 'add') {
      added += 1
    } else if (line.kind === 'del') {
      removed += 1
    }
  }
  return { added, removed }
}

// ClippedDiff is what a diff view draws before the reader asks for the
// rest: the first rows, and what kind of limit stopped them.
export interface ClippedDiff {
  rows: DiffLine[]
  clipped: boolean
  // byChars is true when the character cap, not the row cap, cut the
  // rows: the label then names characters instead of lines.
  byChars: boolean
}

// clipDiffLines clips a diff the way OutputBlock clips a tool result: the
// first maxRows rows, then maxChars characters of them. The row cap alone
// does not bound the work: one row can hold a whole minified line, so the
// character cap cuts inside the last row that fits.
export function clipDiffLines(
  lines: DiffLine[],
  maxRows = maxDiffLines,
  maxChars = maxOutputChars,
): ClippedDiff {
  const rows: DiffLine[] = []
  let left = maxChars
  for (const line of lines) {
    if (rows.length >= maxRows || left <= 0) {
      return { rows, clipped: true, byChars: left <= 0 }
    }
    if (line.text.length > left) {
      rows.push({ ...line, text: clipChars(line.text, left) })
      return { rows, clipped: true, byChars: true }
    }
    rows.push(line)
    left -= line.text.length
  }
  return { rows, clipped: false, byChars: false }
}
