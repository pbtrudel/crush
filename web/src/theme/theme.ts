export type ThemePreference = 'light' | 'dark' | 'system'
export type ResolvedTheme = 'light' | 'dark'

const storageKey = 'crush.theme'

// readPreference answers the stored preference, defaulting to system. The
// browser storage is read inside the try: a browser that blocks site data
// throws a SecurityError from the getter, which must not stop the page from
// painting. Under Node the global is undefined instead, and is covered too.
export function readPreference(storage?: Storage): ThemePreference {
  let raw: string | null
  try {
    raw = (storage ?? localStorage).getItem(storageKey)
  } catch {
    return 'system'
  }
  return raw === 'light' || raw === 'dark' || raw === 'system' ? raw : 'system'
}

export function writePreference(pref: ThemePreference, storage: Storage = localStorage): void {
  storage.setItem(storageKey, pref)
}

export function resolveTheme(pref: ThemePreference, prefersDark: boolean): ResolvedTheme {
  if (pref === 'system') {
    return prefersDark ? 'dark' : 'light'
  }
  return pref
}

export function applyTheme(root: HTMLElement, theme: ResolvedTheme): void {
  root.dataset.dsTheme = theme
}

export function systemPrefersDark(): boolean {
  return window.matchMedia('(prefers-color-scheme: dark)').matches
}
