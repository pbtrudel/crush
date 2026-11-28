// LanguageId names the grammars the lazy highlighter can load. The table
// lives in the first-paint chunk because it is small and every code block
// asks it; the grammars themselves load on demand.
export type LanguageId =
  | 'typescript'
  | 'tsx'
  | 'javascript'
  | 'shellscript'
  | 'json'
  | 'go'
  | 'python'
  | 'rust'
  | 'yaml'
  | 'toml'
  | 'markdown'
  | 'html'
  | 'css'
  | 'sql'
  | 'diff'
  | 'dockerfile'
  | 'make'
  | 'c'
  | 'cpp'
  | 'java'
  | 'ruby'
  | 'lua'
  | 'xml'
  | 'ini'
  | 'graphql'
  | 'proto'

// A Map, not an object: a fence label is assistant-authored, and a label
// such as constructor or __proto__ must miss instead of resolving an
// inherited property and reaching the highlighter.
const aliases = new Map<string, LanguageId>([
  ['typescript', 'typescript'],
  ['ts', 'typescript'],
  ['mts', 'typescript'],
  ['tsx', 'tsx'],
  ['jsx', 'tsx'],
  ['javascript', 'javascript'],
  ['js', 'javascript'],
  ['mjs', 'javascript'],
  ['cjs', 'javascript'],
  ['shellscript', 'shellscript'],
  ['shell', 'shellscript'],
  ['bash', 'shellscript'],
  ['sh', 'shellscript'],
  ['zsh', 'shellscript'],
  ['console', 'shellscript'],
  ['json', 'json'],
  ['jsonc', 'json'],
  ['go', 'go'],
  ['golang', 'go'],
  ['python', 'python'],
  ['py', 'python'],
  ['rust', 'rust'],
  ['rs', 'rust'],
  ['yaml', 'yaml'],
  ['yml', 'yaml'],
  ['toml', 'toml'],
  ['markdown', 'markdown'],
  ['md', 'markdown'],
  ['html', 'html'],
  ['css', 'css'],
  ['sql', 'sql'],
  ['diff', 'diff'],
  ['patch', 'diff'],
  ['dockerfile', 'dockerfile'],
  ['docker', 'dockerfile'],
  ['make', 'make'],
  ['makefile', 'make'],
  ['c', 'c'],
  ['h', 'c'],
  ['cpp', 'cpp'],
  ['c++', 'cpp'],
  ['cc', 'cpp'],
  ['hpp', 'cpp'],
  ['java', 'java'],
  ['ruby', 'ruby'],
  ['rb', 'ruby'],
  ['lua', 'lua'],
  ['xml', 'xml'],
  ['svg', 'xml'],
  ['ini', 'ini'],
  ['graphql', 'graphql'],
  ['gql', 'graphql'],
  ['proto', 'proto'],
  ['protobuf', 'proto'],
])

const fileNames = new Map<string, LanguageId>([
  ['dockerfile', 'dockerfile'],
  ['makefile', 'make'],
  ['gnumakefile', 'make'],
])

export function resolveLanguage(hint: string | null | undefined): LanguageId | null {
  if (!hint) {
    return null
  }
  return aliases.get(hint.trim().toLowerCase()) ?? null
}

export function languageForPath(path: string): LanguageId | null {
  const name = path.slice(path.lastIndexOf('/') + 1).toLowerCase()
  const byName = fileNames.get(name)
  if (byName !== undefined) {
    return byName
  }
  const dot = name.lastIndexOf('.')
  return dot <= 0 ? null : resolveLanguage(name.slice(dot + 1))
}
