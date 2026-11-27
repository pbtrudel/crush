import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'

import './theme/index.css'
import App from './App'
import { applyTheme, readPreference, resolveTheme, systemPrefersDark } from './theme/theme'

const root = document.getElementById('root')
if (!root) {
  throw new Error('the page has no #root element')
}

applyTheme(document.documentElement, resolveTheme(readPreference(), systemPrefersDark()))

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
