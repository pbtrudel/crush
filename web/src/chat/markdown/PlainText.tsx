import styles from './Markdown.module.css'

export interface PlainTextProps {
  source: string
}

// PlainText shows Markdown source verbatim: it is the fallback for a
// source too long or too deep to parse, and for a tree that threw.
export default function PlainText({ source }: PlainTextProps) {
  return <pre className={styles.plainText}>{source}</pre>
}
