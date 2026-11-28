import styles from './CodeBlock.module.css'

export interface CodeBlockProps {
  code: string
  language?: string | null
  // lineStart numbers the first line; lineNumbers turns the gutter on.
  lineStart?: number
  lineNumbers?: boolean
}

// CodeBlock is a fenced block or a file excerpt. It renders plain text;
// highlighting arrives with the lazy Shiki chunk.
export default function CodeBlock({ code, language = null, lineStart = 1, lineNumbers = false }: CodeBlockProps) {
  const lines = code.split('\n')
  return (
    <div className={styles.block}>
      {language ? <div className={styles.label}>{language}</div> : null}
      <pre className={styles.pre}>
        <code>
          {lines.map((line, index) => (
            <span key={index} className={styles.line}>
              {lineNumbers ? (
                <span className={styles.gutter} aria-hidden="true">
                  {lineStart + index}
                </span>
              ) : null}
              {line}
            </span>
          ))}
        </code>
      </pre>
    </div>
  )
}
