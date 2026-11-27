export type ThemePreference = 'light' | 'dark' | 'system'
export type ResolvedTheme = 'light' | 'dark'

const storageKey = 'crush.theme'

export function readPreference(storage: Storage = localStorage): ThemePreference {
  const raw = storage.getItem(storageKey)
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
