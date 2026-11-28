import styles from './Markdown.module.css'

export interface MathFormulaProps {
  tex: string
  display: boolean
}

// MathFormula shows TeX source until the lazy KaTeX chunk renders it.
export default function MathFormula({ tex, display }: MathFormulaProps) {
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
