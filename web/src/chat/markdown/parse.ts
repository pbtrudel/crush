// Lifted from deepseek-harness
// packages/client/ui-primitives/src/markdown/parse.ts at
// 4b399c39a6d1479cd84ca591b4d7c88e24d38a96. MIT license, see
// web/THIRD_PARTY_NOTICES.md. Changes: one grammar (GFM plus math) instead
// of a streaming and a settled arm; the CJK-strong, local-image,
// Windows-path and math-compatibility extensions were dropped; inline math
// needs $$ because a coding chat writes prices far more often than TeX.
import type { Root } from 'mdast'
import { fromMarkdown } from 'mdast-util-from-markdown'
import { gfmFromMarkdown } from 'mdast-util-gfm'
import { mathFromMarkdown } from 'mdast-util-math'
import { gfm } from 'micromark-extension-gfm'
import { math } from 'micromark-extension-math'

// GFM strikethrough requires ~~; a single ~ stays literal, as in 10~20.
const gfmOptions = { singleTilde: false }

export function parseMarkdown(source: string): Root {
  return fromMarkdown(source, {
    extensions: [gfm(gfmOptions), math({ singleDollarTextMath: false })],
    mdastExtensions: [gfmFromMarkdown(), mathFromMarkdown()],
  })
}
