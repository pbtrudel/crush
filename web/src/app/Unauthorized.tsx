import styles from './Unauthorized.module.css'

// Unauthorized is the screen a tab without a credential shows. The token is
// never printed: the only way forward is a fresh launch link.
export default function Unauthorized() {
  return (
    <main className={styles.screen}>
      <h1>Crush web mode</h1>
      <p>
        This tab has no valid token. Run <code>/web</code> in the terminal to open a new link.
      </p>
    </main>
  )
}
