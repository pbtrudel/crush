import styles from './Markdown.module.css'

export interface MathSourceProps {
  tex: string
  display: boolean
}

// MathSource is the TeX fallback. The eager frame shows it while the lazy
// KaTeX chunk loads, and both the frame and the view fall back to it for
// TeX that KaTeX cannot parse or that is too long to typeset.
export default function MathSource({ tex, display }: MathSourceProps) {
  if (display) {
    return (
      <pre className={styles.mathSource} data-math="display">
        <code>{tex}</code>
      </pre>
    )
  }
  return (
    <code className={styles.mathSource} data-math="inline">
      {tex}
    </code>
  )
}
