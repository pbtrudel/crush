import { clsx } from 'clsx'
import {
  useEffect,
  useRef,
  useState,
  type ChangeEvent,
  type PointerEvent as ReactPointerEvent,
  type ReactNode,
} from 'react'

import {
  applyTheme,
  readPreference,
  resolveTheme,
  writePreference,
  type ThemePreference,
} from '../theme/theme'
import styles from './AppFrame.module.css'
import { clampSidebarWidth, sidebarDefaultWidth } from './sidebarWidth'

export interface AppFrameProps {
  sidebar: ReactNode
  right?: ReactNode
  children: ReactNode
  // storage carries the theme preference; a test injects an in-memory one and
  // the browser falls back to localStorage.
  storage?: Storage
}

const narrowQuery = '(max-width: 767px)'
const darkQuery = '(prefers-color-scheme: dark)'
const focusableSelector =
  'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])'

// resolveStorage never lets a blocked storage throw out of the frame: the
// preference then lasts for the page only.
function resolveStorage(storage?: Storage): Storage | undefined {
  if (storage) {
    return storage
  }
  try {
    return window.localStorage as Storage | undefined
  } catch {
    return undefined
  }
}

function storedPreference(storage: Storage | undefined): ThemePreference {
  if (!storage) {
    return 'system'
  }
  try {
    return readPreference(storage)
  } catch {
    return 'system'
  }
}

function rememberPreference(storage: Storage | undefined, preference: ThemePreference): void {
  if (!storage) {
    return
  }
  try {
    writePreference(preference, storage)
  } catch {
    // A blocked storage keeps the preference for the page only.
  }
}

export default function AppFrame({ sidebar, right, children, storage }: AppFrameProps) {
  const [collapsed, setCollapsed] = useState(false)
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [narrow, setNarrow] = useState(() => window.matchMedia(narrowQuery).matches)
  const [width, setWidth] = useState(sidebarDefaultWidth)
  const store = resolveStorage(storage)
  const [preference, setPreference] = useState<ThemePreference>(() => storedPreference(store))
  const frame = useRef<HTMLDivElement>(null)
  const panel = useRef<HTMLDivElement>(null)
  const toggle = useRef<HTMLButtonElement>(null)

  // The width rides a custom property on the frame element, so a drag updates
  // the layout without re-rendering the tree and the CSP sees no inline
  // style attribute in the markup.
  useEffect(() => {
    frame.current?.style.setProperty('--crush-sidebar-width', `${width}px`)
  }, [width])

  // The breakpoint is a live query: a window narrowed past it opens the
  // sidebar as a drawer, and a window widened past it goes back to columns.
  useEffect(() => {
    const media = window.matchMedia(narrowQuery)
    const onChange = () => setNarrow(media.matches)
    media.addEventListener('change', onChange)
    return () => media.removeEventListener('change', onChange)
  }, [])

  // The theme follows the preference, and while the preference is system it
  // follows the operating system too. The listener comes off on unmount.
  useEffect(() => {
    const media = window.matchMedia(darkQuery)
    const paint = (value: ThemePreference) => {
      applyTheme(document.documentElement, resolveTheme(value, media.matches))
    }
    paint(preference)
    const onChange = () => {
      if (storedPreference(store) === 'system') {
        paint('system')
      }
    }
    media.addEventListener('change', onChange)
    return () => media.removeEventListener('change', onChange)
  }, [preference, store])

  // A drag installs two window listeners; releasing or unmounting takes them
  // off, so a drag that ends outside the window still cleans up.
  const stopDrag = useRef<(() => void) | null>(null)
  useEffect(() => () => stopDrag.current?.(), [])

  const startResize = (event: ReactPointerEvent<HTMLDivElement>) => {
    event.preventDefault()
    const originX = event.clientX
    const originWidth = width
    const move = (moveEvent: PointerEvent) => {
      setWidth(clampSidebarWidth(originWidth + (moveEvent.clientX - originX)))
    }
    const stop = () => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', stop)
      stopDrag.current = null
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', stop)
    stopDrag.current = stop
  }

  const choosePreference = (event: ChangeEvent<HTMLSelectElement>) => {
    const next = event.target.value as ThemePreference
    rememberPreference(store, next)
    applyTheme(document.documentElement, resolveTheme(next, window.matchMedia(darkQuery).matches))
    setPreference(next)
  }

  // The drawer is modal: focus moves into it, Tab cycles inside it, and
  // Escape closes it and returns focus to the toggle.
  useEffect(() => {
    if (!drawerOpen || panel.current === null) {
      return
    }
    const element = panel.current
    element.focus()
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setDrawerOpen(false)
        toggle.current?.focus()
        return
      }
      if (event.key !== 'Tab') {
        return
      }
      const focusable = element.querySelectorAll<HTMLElement>(focusableSelector)
      if (focusable.length === 0) {
        return
      }
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault()
        last.focus()
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault()
        first.focus()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [drawerOpen])

  const sidebarHidden = narrow ? !drawerOpen : collapsed

  return (
    <div className={styles.app}>
      <div className={styles.banner}>
        <p className={styles.bannerText}>Preview: the new web UI is incomplete</p>
        <label className={styles.theme}>
          <span>Theme</span>
          <select className={styles.themeSelect} value={preference} onChange={choosePreference}>
            <option value="light">Light</option>
            <option value="dark">Dark</option>
            <option value="system">System</option>
          </select>
        </label>
      </div>
      <div
        ref={frame}
        className={styles.frame}
        data-testid="app-frame"
        data-collapsed={collapsed ? 'true' : 'false'}
        data-drawer={drawerOpen ? 'true' : 'false'}
      >
        <div
          ref={panel}
          className={clsx(styles.sidebar, collapsed && !narrow && styles.rail, narrow && drawerOpen && styles.drawer)}
          tabIndex={-1}
          role={narrow && drawerOpen ? 'dialog' : undefined}
          aria-modal={narrow && drawerOpen ? true : undefined}
          aria-label={narrow && drawerOpen ? 'Sessions' : undefined}
        >
          <button
            ref={toggle}
            type="button"
            className={styles.collapse}
            onClick={() => (narrow ? setDrawerOpen((value) => !value) : setCollapsed((value) => !value))}
            aria-label={sidebarHidden ? 'Open sidebar' : 'Collapse sidebar'}
            aria-expanded={!sidebarHidden}
          >
            {sidebarHidden ? '»' : '«'}
          </button>
          {sidebarHidden ? null : sidebar}
        </div>
        <div
          className={styles.resizer}
          role="separator"
          aria-orientation="vertical"
          onPointerDown={startResize}
        />
        <main className={styles.center}>{children}</main>
        {right ? <aside className={styles.right}>{right}</aside> : null}
      </div>
    </div>
  )
}
