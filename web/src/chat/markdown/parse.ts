// Lifted from deepseek-harness
// packages/client/ui-primitives/src/markdown/parse.ts at
// 4b399c39a6d1479cd84ca591b4d7c88e24d38a96. MIT license, see
// web/THIRD_PARTY_NOTICES.md. Changes: one grammar (GFM plus math) instead
// of a streaming and a settled arm; the CJK-strong, local-image,
// Windows-path and math-compatibility extensions were dropped; inline math
// needs $$ because a coding chat writes prices far more often than TeX;
// plus the depth guard below, which the renderer needs before it walks the
// tree.
import type { Nodes, Root } from 'mdast'
import { fromMarkdown } from 'mdast-util-from-markdown'
import { gfmFromMarkdown } from 'mdast-util-gfm'
import { mathFromMarkdown } from 'mdast-util-math'
import { gfm } from 'micromark-extension-gfm'
import { math } from 'micromark-extension-math'

// GFM strikethrough requires ~~; a single ~ stays literal, as in 10~20.
const gfmOptions = { singleTilde: false }

// maxMarkdownDepth bounds how deep a parsed tree may nest. The renderer
// walks one stack frame per level, so a deeper tree renders as text.
export const maxMarkdownDepth = 64

export function parseMarkdown(source: string): Root {
  return fromMarkdown(source, {
    extensions: [gfm(gfmOptions), math({ singleDollarTextMath: false })],
    mdastExtensions: [gfmFromMarkdown(), mathFromMarkdown()],
  })
}

// treeDepth answers the deepest node's depth, counting the root as 1. The
// walk keeps its own stack: measuring a pathological tree must not recurse,
// or the measurement itself would overflow.
export function treeDepth(root: Root): number {
  let deepest = 0
  const stack: { node: Nodes; depth: number }[] = [{ node: root, depth: 1 }]
  while (stack.length > 0) {
    const entry = stack.pop()
    if (entry === undefined) {
      break
    }
    deepest = Math.max(deepest, entry.depth)
    if ('children' in entry.node) {
      for (const child of entry.node.children) {
        stack.push({ node: child, depth: entry.depth + 1 })
      }
    }
  }
  return deepest
}
