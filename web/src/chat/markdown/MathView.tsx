import katex from 'katex'
import { useMemo } from 'react'

import MathSource from './MathSource'
import { mathTree, maxMathSource, renderMathNode } from './mathml'
import styles from './Markdown.module.css'

export interface MathViewProps {
  tex: string
  display: boolean
}

// MathView is the lazy chunk: KaTeX plus the MathML sanitizer. trust is
// false so \href, \url and \htmlClass never produce anything, and a TeX
// error shows the source instead of KaTeX's red, inline-styled message.
export default function MathView({ tex, display }: MathViewProps) {
  const tree = useMemo(() => {
    if (tex.length > maxMathSource) {
      return null
    }
    try {
      return mathTree(
        katex.renderToString(tex, {
          output: 'mathml',
          displayMode: display,
          throwOnError: true,
          trust: false,
          strict: 'ignore',
        }),
      )
    } catch {
      return null
    }
  }, [tex, display])
  if (tree === null) {
    return <MathSource tex={tex} display={display} />
  }
  return (
    <span className={display ? styles.mathDisplay : styles.mathInline} data-math={display ? 'display' : 'inline'}>
      {renderMathNode(tree, 0)}
    </span>
  )
}
