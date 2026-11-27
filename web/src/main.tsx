import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'

import './theme/index.css'
import App from './App'
import { applyTheme, readPreference, resolveTheme, systemPrefersDark } from './theme/theme'

const root = document.getElementById('root')
if (!root) {
  throw new Error('the page has no #root element')
}

// The theme is painted before React renders. A browser that blocks site data
// throws from the storage getter, and a blank page is worse than the wrong
// theme: the system preference decides for this page only.
try {
  applyTheme(document.documentElement, resolveTheme(readPreference(), systemPrefersDark()))
} catch {
  applyTheme(document.documentElement, systemPrefersDark() ? 'dark' : 'light')
}

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
