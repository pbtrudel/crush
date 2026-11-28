import { useEffect, useRef, useState } from 'react'

import type { HighlightedToken } from './highlighter'
import { resolveLanguage, type LanguageId } from './languages'

export interface Highlight {
  // lines are the highlighted lines; covered is how much of the code they
  // stand for. A streamed block is longer than its last pass, and the rest
  // renders plain until the next pass lands.
  lines: HighlightedToken[][]
  covered: number
}

// maxHighlightSource caps what one block may cost to tokenize: a pass over
// 32 KiB takes about 107 ms, and a streamed block pays it again on every
// pass. Past the cap the block renders plain and is never tokenized.
export const maxHighlightSource = 16 * 1024

// highlightThrottleMs is the least time between two passes over a block
// whose text keeps changing. The timer is trailing, so the final text
// always gets its pass.
export const highlightThrottleMs = 150

let module: Promise<typeof import('./highlighter')> | null = null

// loadHighlighter is the one dynamic import of the Shiki chunk. A chunk
// that failed to load is not cached, so a later block can retry it.
export function loadHighlighter(): Promise<typeof import('./highlighter')> {
  module ??= import('./highlighter').catch((error: unknown) => {
    module = null
    throw error
  })
  return module
}

interface Pass {
  code: string
  lang: LanguageId
  lines: HighlightedToken[][]
}

export function useHighlight(code: string, language: string | null): Highlight | null {
  const lang = resolveLanguage(language)
  const [pass, setPass] = useState<Pass | null>(null)
  const lastPass = useRef(0)

  useEffect(() => {
    if (lang === null || code.length > maxHighlightSource) {
      return
    }
    let live = true
    const run = () => {
      lastPass.current = Date.now()
      loadHighlighter()
        .then((highlighter) => highlighter.tokenize(code, lang))
        .then(
          (lines) => {
            if (live) {
              setPass({ code, lang, lines })
            }
          },
          // A grammar that cannot load leaves the block plain.
          () => undefined,
        )
    }
    // A streamed block changes on every delta, and each pass re-tokenizes
    // its whole text: run at most one pass per highlightThrottleMs. The
    // timer is trailing, so the last text always gets a pass, and the
    // cleanup drops a pass that a later change or the unmount outdates.
    const wait = highlightThrottleMs - (Date.now() - lastPass.current)
    if (wait <= 0) {
      run()
      return () => {
        live = false
      }
    }
    const timer = window.setTimeout(run, wait)
    return () => {
      live = false
      window.clearTimeout(timer)
    }
  }, [code, lang])

  if (lang === null || code.length > maxHighlightSource) {
    return null
  }
  if (pass === null || pass.lang !== lang || !code.startsWith(pass.code)) {
    return null
  }
  if (pass.code === code) {
    return { lines: pass.lines, covered: code.length }
  }
  // The block grew at its end. Every line of the last pass but its last is
  // final; the last one may still be growing, so it renders plain.
  const complete = pass.code.lastIndexOf('\n') + 1
  const kept = complete === 0 ? 0 : pass.code.slice(0, complete).split('\n').length - 1
  return { lines: pass.lines.slice(0, kept), covered: complete }
}
