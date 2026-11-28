import { describe, expect, it } from 'vitest'

import { languageForPath, resolveLanguage } from './languages'

describe('resolveLanguage', () => {
  it('resolves fence labels and their aliases, any case', () => {
    expect(resolveLanguage('go')).toBe('go')
    expect(resolveLanguage('TS')).toBe('typescript')
    expect(resolveLanguage('sh')).toBe('shellscript')
    expect(resolveLanguage('yml')).toBe('yaml')
  })

  it('misses on a label that names an inherited property or nothing', () => {
    expect(resolveLanguage('constructor')).toBeNull()
    expect(resolveLanguage('__proto__')).toBeNull()
    expect(resolveLanguage('brainfudge')).toBeNull()
    expect(resolveLanguage('')).toBeNull()
    expect(resolveLanguage(null)).toBeNull()
  })
})

describe('languageForPath', () => {
  it('reads the extension, then the file name', () => {
    expect(languageForPath('/tmp/project/parser.go')).toBe('go')
    expect(languageForPath('web/src/App.tsx')).toBe('tsx')
    expect(languageForPath('Dockerfile')).toBe('dockerfile')
    expect(languageForPath('/repo/Makefile')).toBe('make')
    expect(languageForPath('/repo/notes')).toBeNull()
  })
})
