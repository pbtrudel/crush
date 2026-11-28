import { useEffect, useState } from 'react'

import type { HighlightedToken } from './highlighter'
import { resolveLanguage, type LanguageId } from './languages'

export interface Highlight {
  // lines are the highlighted lines; covered is how much of the code they
  // stand for. A streamed block is longer than its last pass, and the rest
  // renders plain until the next pass lands.
  lines: HighlightedToken[][]
  covered: number
}

let module: Promise<typeof import('./highlighter')> | null = null

// loadHighlighter is the one dynamic import of the Shiki chunk.
export function loadHighlighter(): Promise<typeof import('./highlighter')> {
  module ??= import('./highlighter')
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

  useEffect(() => {
    if (lang === null) {
      return
    }
    let live = true
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
    return () => {
      live = false
    }
  }, [code, lang])

  if (lang === null || pass === null || pass.lang !== lang || !code.startsWith(pass.code)) {
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
