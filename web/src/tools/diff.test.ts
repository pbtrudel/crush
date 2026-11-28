import { describe, expect, it } from 'vitest'

import { contrast, rgbOf, vars } from '../theme/contrast'
import design from '../theme/design-platform.css?raw'
import view from './DiffView.module.css?raw'
import {
  clipDiffLines,
  diffContents,
  diffFromPatch,
  diffStats,
  diffVersions,
  maxDiffInputChars,
  maxDiffLines,
} from './diff'

describe('diffContents', () => {
  it('numbers both sides and marks the change', () => {
    const lines = diffContents(
      'package parser\n\nfunc Parse() {}\n',
      'package parser\n\nfunc Parse() error { return nil }\n',
    )
    expect(lines[0]).toMatchObject({ kind: 'hunk' })
    expect(lines.filter((line) => line.kind === 'del')).toEqual([
      { kind: 'del', text: 'func Parse() {}', oldNumber: 3, newNumber: null },
    ])
    expect(lines.filter((line) => line.kind === 'add')).toEqual([
      { kind: 'add', text: 'func Parse() error { return nil }', oldNumber: null, newNumber: 3 },
    ])
    expect(diffStats(lines)).toEqual({ added: 1, removed: 1 })
  })

  it('answers no lines for equal text', () => {
    expect(diffContents('same\n', 'same\n')).toEqual([])
  })

  // A file over 20000 characters is diffed whole: a cut at a fixed offset
  // shows a phantom hunk at the cut, or no change at all when the edit is
  // past it. The pin is a real hunk with the real line numbers.
  it('diffs a change past character 20000', () => {
    const before = Array.from({ length: 1000 }, (_, index) => `const value${index} = compute(${index})`).join('\n')
    const after = Array.from({ length: 1000 }, (_, index) =>
      index === 900 ? 'const value900 = replaced(900)' : `const value${index} = compute(${index})`,
    ).join('\n')
    expect(before.length).toBeGreaterThan(20000)
    const lines = diffContents(before, after)
    expect(lines.filter((line) => line.kind === 'hunk')).toHaveLength(1)
    expect(lines.filter((line) => line.kind === 'del')).toEqual([
      { kind: 'del', text: 'const value900 = compute(900)', oldNumber: 901, newNumber: null },
    ])
    expect(lines.filter((line) => line.kind === 'add')).toEqual([
      { kind: 'add', text: 'const value900 = replaced(900)', oldNumber: null, newNumber: 901 },
    ])
  })

  // A -y/+y pair with a trailing newline changed has no visible reason
  // without the marker; it describes the line above it.
  it('keeps the no-newline marker as a row', () => {
    const lines = diffContents('a\ny', 'a\ny\n')
    expect(lines.map((line) => line.kind)).toEqual(['hunk', 'context', 'del', 'marker', 'add'])
    expect(lines[3]).toEqual({
      kind: 'marker',
      text: '\\ No newline at end of file',
      oldNumber: null,
      newNumber: null,
    })
    expect(diffStats(lines)).toEqual({ added: 1, removed: 1 })
  })

  // The row that was open is unmounted and mounted again by the
  // virtualized log; the same strings answer the same lines object, so
  // the second mount does not diff again.
  it('answers the same lines object for the same inputs', () => {
    const again = diffContents('one\ntwo\n', 'one\nthree\n')
    expect(diffContents('one\ntwo\n', 'one\nthree\n')).toBe(again)
  })
})

describe('diffVersions', () => {
  it('answers the lines of a change it can afford', () => {
    const answer = diffVersions('one\ntwo\n', 'one\n2\n')
    expect(answer.tooLarge).toBe(false)
    expect(diffStats(answer.lines)).toEqual({ added: 1, removed: 1 })
  })

  // Two files that share no line make Myers's pass quadratic, and 20000
  // characters of that take 21.5 s. The edit-distance cap gives up instead.
  it('gives up on two versions that share no line', () => {
    const before = 'a\n'.repeat(3000)
    const after = 'b\n'.repeat(3000)
    const started = performance.now()
    const answer = diffVersions(before, after)
    expect(answer.tooLarge).toBe(true)
    expect(answer.lines).toEqual([])
    expect(performance.now() - started).toBeLessThan(2000)
  })

  it('keeps a whole file that was rewritten line by line', () => {
    const before = Array.from({ length: 200 }, (_, index) => `before line ${index}`).join('\n')
    const after = Array.from({ length: 200 }, (_, index) => `after line ${index}`).join('\n')
    const answer = diffVersions(before, after)
    expect(answer.tooLarge).toBe(false)
    expect(diffStats(answer.lines)).toEqual({ added: 200, removed: 200 })
  })
})

describe('the memory guard', () => {
  it('diffs a file far over 20000 characters', () => {
    const before = Array.from({ length: 9000 }, (_, index) => `line ${index}`).join('\n')
    expect(before.length).toBeGreaterThan(20000)
    expect(diffVersions(before, `${before}\nlast`).tooLarge).toBe(false)
  })

  // A version over a mebibyte is not diffed at all; the view shows the new
  // text instead. The guard reads lengths, so it costs nothing.
  it('refuses a version over a mebibyte', () => {
    const big = `${'x'.repeat(maxDiffInputChars)}y`
    const started = performance.now()
    expect(diffVersions(big, 'small')).toEqual({ lines: [], tooLarge: true })
    expect(diffVersions('small', big)).toEqual({ lines: [], tooLarge: true })
    expect(performance.now() - started).toBeLessThan(50)
  })

  it('still diffs a version just under the guard', () => {
    const big = 'x'.repeat(maxDiffInputChars - 10)
    expect(diffVersions(`${big}tail`, `${big}tale`).tooLarge).toBe(false)
  })
})

describe('clipDiffLines', () => {
  const rows = (count: number) =>
    Array.from({ length: count }, (_, index) => ({
      kind: 'context' as const,
      text: `line ${index + 1}`,
      oldNumber: index + 1,
      newNumber: index + 1,
    }))

  it('keeps a short diff whole', () => {
    const lines = rows(3)
    expect(clipDiffLines(lines)).toEqual({ rows: lines, clipped: false, byChars: false })
  })

  it('clips at forty lines', () => {
    const clipped = clipDiffLines(rows(maxDiffLines + 5))
    expect(clipped.rows).toHaveLength(maxDiffLines)
    expect(clipped.clipped).toBe(true)
    expect(clipped.byChars).toBe(false)
  })

  // The line cap cannot cut one enormous row, so the character cap does.
  it('clips at 20000 characters when one row is longer', () => {
    const lines = [{ kind: 'add' as const, text: 'x'.repeat(50000), oldNumber: null, newNumber: 1 }]
    const clipped = clipDiffLines(lines)
    expect(clipped.rows).toHaveLength(1)
    expect(clipped.rows[0].text).toHaveLength(20000)
    expect(clipped.clipped).toBe(true)
    expect(clipped.byChars).toBe(true)
  })

  it('stops at the character cap across rows', () => {
    const lines = Array.from({ length: 10 }, (_, index) => ({
      kind: 'context' as const,
      text: 'y'.repeat(5000),
      oldNumber: index + 1,
      newNumber: index + 1,
    }))
    const clipped = clipDiffLines(lines)
    expect(clipped.rows).toHaveLength(4)
    expect(clipped.clipped).toBe(true)
    expect(clipped.byChars).toBe(true)
  })
})

describe('diffFromPatch', () => {
  it('reads the unified diff the write tool stores', () => {
    const patch = '--- a/notes.md\n+++ b/notes.md\n@@ -1,2 +1,2 @@\n # Notes\n-old line\n+new line\n'
    const lines = diffFromPatch(patch)
    expect(lines.map((line) => line.kind)).toEqual(['hunk', 'context', 'del', 'add'])
    expect(lines[3]).toMatchObject({ text: 'new line', newNumber: 2 })
  })

  it('answers no lines for text that is not a patch', () => {
    expect(diffFromPatch('')).toEqual([])
  })

  // The write tool's diff of a new file starts at -0,0; jsdiff's parser
  // normalises that to an empty old side at line one.
  it('numbers a created file from one', () => {
    const lines = diffFromPatch('--- a/notes.md\n+++ b/notes.md\n@@ -0,0 +1,2 @@\n+# Notes\n+new line\n')
    expect(lines).toEqual([
      { kind: 'hunk', text: '@@ -1,0 +1,2 @@', oldNumber: null, newNumber: null },
      { kind: 'add', text: '# Notes', oldNumber: null, newNumber: 1 },
      { kind: 'add', text: 'new line', oldNumber: null, newNumber: 2 },
    ])
  })
})

describe('diffStats', () => {
  it('counts additions and removals only', () => {
    const lines = diffContents('one\ntwo\nthree\n', 'one\n2\nthree\n')
    expect(diffStats(lines)).toEqual({ added: 1, removed: 1 })
    expect(diffStats([])).toEqual({ added: 0, removed: 0 })
  })
})

// token reads one custom property of a class rule from the module
// stylesheet, the way ansi.test.ts reads a colour declaration.
function token(sheet: string, name: string): string | null {
  let found: string | null = null
  const source = sheet.replace(/\/\*[\s\S]*?\*\//g, '')
  for (const block of source.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    if (block[1].includes('data-ds-theme')) {
      continue
    }
    const value = new RegExp(`--${name}:\\s*([^;]+);`).exec(block[2])
    if (value) {
      found = value[1].trim()
    }
  }
  return found
}

// rule reads one property of one .class rule, so a test can pin which
// token a class spends.
function rule(sheet: string, property: 'color' | 'background', name: string): string | null {
  let found: string | null = null
  const source = sheet.replace(/\/\*[\s\S]*?\*\//g, '')
  for (const block of source.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    const selectors = block[1].split(',').map((selector) => selector.trim())
    if (!selectors.includes(`.${name}`)) {
      continue
    }
    const value = new RegExp(`${property}:\\s*([^;]+);`).exec(block[2])
    if (value) {
      found = value[1].trim()
    }
  }
  return found
}

describe('the diff colours', () => {
  const sheet = new Map([...vars(design, 'light'), ...vars(view, 'light')])

  it('draws the rows from the module palette', () => {
    expect(rule(view, 'color', 'add')).toBe('var(--diff-add-fg)')
    expect(rule(view, 'background', 'add')).toBe('var(--diff-add-bg)')
    expect(rule(view, 'color', 'del')).toBe('var(--diff-del-fg)')
    expect(rule(view, 'background', 'del')).toBe('var(--diff-del-bg)')
  })

  // The card is white in the light theme, and an added row sits on layer
  // one, the deletion row on a red wash over the card. Text on either
  // must reach WCAG AA, or a diff is unreadable in the light theme.
  it('keeps the light additions at 4.5:1 against their row', () => {
    const addFg = token(view, 'diff-add-fg')
    const addBg = token(view, 'diff-add-bg')
    expect(addFg).not.toBeNull()
    expect(addBg).not.toBeNull()
    expect(rgbOf(addFg ?? '', sheet)).toBeDefined()
    expect(contrast(rgbOf(addFg ?? '', sheet), rgbOf(addBg ?? '', sheet))).toBeGreaterThanOrEqual(4.5)
  })

  it('keeps the light removals at 4.5:1 against their row', () => {
    const delFg = token(view, 'diff-del-fg')
    const delBg = token(view, 'diff-del-bg')
    expect(delFg).not.toBeNull()
    expect(delBg).not.toBeNull()
    expect(contrast(rgbOf(delFg ?? '', sheet), rgbOf(delBg ?? '', sheet))).toBeGreaterThanOrEqual(4.5)
  })

  it('names the diff rows in classes, never in a style attribute', () => {
    expect(view).not.toMatch(/\bstyle\s*=/)
  })
})
