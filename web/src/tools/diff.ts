import { parsePatch, structuredPatch, type StructuredPatchHunk } from 'diff'

import { clipChars, maxOutputChars } from './clip'

// DiffLine is one row of a unified diff: the hunk header, an added line, a
// removed line, a line both versions share, or a marker such as "\ No
// newline at end of file", which describes the line above it. A side that
// has no such line carries null, so the gutter shows nothing rather than a
// wrong number.
export interface DiffLine {
  kind: 'hunk' | 'add' | 'del' | 'context' | 'marker'
  text: string
  oldNumber: number | null
  newNumber: number | null
}

// maxDiffInputChars is a memory guard, not a clip: a version over a
// mebibyte is not diffed at all and the view shows the new text instead.
// The file is diffed whole below it, because a cut at a fixed offset shows
// a phantom hunk at the cut, or no change at all when the edit is past it.
export const maxDiffInputChars = 1024 * 1024

// maxDiffEditLength bounds how different the two versions may be, in
// changed lines, before jsdiff gives up and answers undefined. Myers's
// pass costs the line count times the edit distance, so two 20000
// character files that share no line take 21.5 s to diff unbounded.
// Measured on this machine: 132 ms at the 20 KB pair, 184 ms at 338 KB
// and 303 ms at the 1 MiB guard (all of them giving up), 1.5 s at 4000
// unbounded; a 13000 character file whose 200 lines were all rewritten is
// 402 apart and diffs in 10 ms. A thousand keeps a whole-file rewrite
// readable while the worst row still answers in about a third of a second.
export const maxDiffEditLength = 1000

// maxDiffLines is the line clip a diff shares with an output block: a diff
// longer than this is drawn down to it until the reader asks for the rest.
export const maxDiffLines = 40

// diffInputTooLarge is the memory guard: true when either version is over
// maxDiffInputChars, where the diff is refused rather than clipped. It
// reads lengths only, so asking costs nothing.
export function diffInputTooLarge(before: string, after: string): boolean {
  return before.length > maxDiffInputChars || after.length > maxDiffInputChars
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
        // "\ No newline at end of file" describes the line above. It is a
        // row of its own, so a -y/+y pair with no other difference shows
        // why it changed.
        out.push({ kind: 'marker', text: line, oldNumber: null, newNumber: null })
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

// diffVersions diffs two versions of a file in the tab, whole, within the
// memory guard and the edit-distance cap. tooLarge is true when the
// versions are too big to hold or too far apart to pass: the view then
// says so instead of drawing a wrong diff.
//
// The answer is not cached here. An edit row asks through the environment's
// per-call cache, which is what keeps a row the virtualized log unmounts and
// mounts again from running jsdiff a second time; a module-level slot would
// hold one answer at a time and be evicted by the next row.
export function diffVersions(before: string, after: string, context = 3): DiffVersions {
  if (diffInputTooLarge(before, after)) {
    return { lines: [], tooLarge: true }
  }
  const patch = structuredPatch('a', 'b', before, after, '', '', {
    context,
    maxEditLength: maxDiffEditLength,
  })
  return patch === undefined ? { lines: [], tooLarge: true } : { lines: hunkLines(patch.hunks), tooLarge: false }
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
