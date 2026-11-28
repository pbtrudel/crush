// Parts lifted from deepseek-harness
// packages/client/ui-primitives/src/markdown/highlight.ts at
// 4b399c39a6d1479cd84ca591b4d7c88e24d38a96. MIT license, see
// web/THIRD_PARTY_NOTICES.md. Lifted: the css-variables theme, the
// JavaScript regex engine and patchGoGrammar (verbatim; its comment records
// the @shikijs/langs 4.6.0 check). Changes: an async core loaded by dynamic
// import instead of a boot-time singleton, a smaller grammar table, and
// tokens mapped onto tok-* class names instead of inline styles, because
// the gateway's CSP allows no inline style.
import {
  createCssVariablesTheme,
  createHighlighterCore,
  type HighlighterCore,
  type LanguageRegistration,
  type ThemedToken,
} from 'shiki/core'
import { createJavaScriptRegexEngine } from 'shiki/engine/javascript'

import type { LanguageId } from './languages'

type Grammar = LanguageRegistration[]

/**
 * Replace the Go grammar's repeated one-line struct-field rule with the rule
 * worlpaker/go-syntax merged in 4a9b120a52c
 * (https://github.com/worlpaker/go-syntax/pull/25): the repeated rule
 * backtracks exponentially on lines such as a brace-bearing comment after
 * `struct {`. Delete this patch once `@shikijs/langs` ships that commit;
 * 4.6.0 does not. The rule is the only top-level rule in
 * `struct_variables_types_fields` whose match carries the field-token
 * literal `[^"/`\s]+;?`. Checked against `@shikijs/langs` 4.6.0, where that
 * rule is still the `(?<=\{) (?:…)+ \s* (?=\})` pattern carrying the literal
 * once, so the count is exactly one. Any other count throws, which makes an
 * upgrade that reshapes the rule loud instead of silently unpatched.
 */
export function patchGoGrammar(grammars: Grammar): Grammar {
  const go = grammars.find((grammar) => grammar.name === 'go')
  const fields = go?.repository.struct_variables_types_fields
  const patterns = fields?.patterns ?? []
  const rules = patterns.filter((rule) => typeof rule.match === 'string' && rule.match.includes('[^"/`\\s]+;?'))
  if (go === undefined || fields === undefined || rules.length !== 1) {
    throw new Error(
      `highlighter: expected exactly one Go struct-field rule to patch for worlpaker/go-syntax#25, found ${rules.length}; update or delete the patch`,
    )
  }
  const repository = { ...go.repository }
  repository.struct_variables_types_fields = {
    ...fields,
    patterns: patterns.map((rule) =>
      rule === rules[0] ? { ...rule, match: '(?:(?<=\\{)([^/\\`"]*[^\\s/\\`"])\\s*(?=\\}))' } : rule,
    ),
  }
  return grammars.map((grammar) => (grammar === go ? { ...go, repository } : grammar))
}

// Each grammar is its own dynamic import, so Vite emits one chunk per
// language and a session pays only for the languages it shows.
const grammars: Record<LanguageId, () => Promise<{ default: Grammar }>> = {
  typescript: () => import('@shikijs/langs/typescript'),
  tsx: () => import('@shikijs/langs/tsx'),
  javascript: () => import('@shikijs/langs/javascript'),
  shellscript: () => import('@shikijs/langs/shellscript'),
  json: () => import('@shikijs/langs/json'),
  go: async () => ({ default: patchGoGrammar((await import('@shikijs/langs/go')).default) }),
  python: () => import('@shikijs/langs/python'),
  rust: () => import('@shikijs/langs/rust'),
  yaml: () => import('@shikijs/langs/yaml'),
  toml: () => import('@shikijs/langs/toml'),
  markdown: () => import('@shikijs/langs/markdown'),
  html: () => import('@shikijs/langs/html'),
  css: () => import('@shikijs/langs/css'),
  sql: () => import('@shikijs/langs/sql'),
  diff: () => import('@shikijs/langs/diff'),
  dockerfile: () => import('@shikijs/langs/dockerfile'),
  make: () => import('@shikijs/langs/make'),
  c: () => import('@shikijs/langs/c'),
  cpp: () => import('@shikijs/langs/cpp'),
  java: () => import('@shikijs/langs/java'),
  ruby: () => import('@shikijs/langs/ruby'),
  lua: () => import('@shikijs/langs/lua'),
  xml: () => import('@shikijs/langs/xml'),
  ini: () => import('@shikijs/langs/ini'),
  graphql: () => import('@shikijs/langs/graphql'),
  proto: () => import('@shikijs/langs/proto'),
}

export interface HighlightedToken {
  text: string
  className: string
}

const theme = createCssVariablesTheme({ name: 'css-variables', variablePrefix: '--shiki-', fontStyle: true })

// A line longer than this is left untokenized: one minified line must not
// stall the tab.
const maxLineLength = 1000

let core: Promise<HighlighterCore> | null = null
const loading = new Map<LanguageId, Promise<void>>()

// highlighter builds the core once. A core that failed to build is not
// cached: the next block tries again instead of inheriting the failure.
function highlighter(): Promise<HighlighterCore> {
  core ??= createHighlighterCore({
    themes: [theme],
    langs: [],
    engine: createJavaScriptRegexEngine({ forgiving: true }),
  }).catch((error: unknown) => {
    core = null
    throw error
  })
  return core
}

// ensure loads a grammar once. A grammar that fails to load stays failed:
// its blocks render plain, and the tab does not retry on every token.
async function ensure(lang: LanguageId): Promise<HighlighterCore> {
  const instance = await highlighter()
  let pending = loading.get(lang)
  if (pending === undefined) {
    pending = grammars[lang]().then((module) => instance.loadLanguage(module.default))
    loading.set(lang, pending)
  }
  await pending
  return instance
}

export async function tokenize(code: string, lang: LanguageId): Promise<HighlightedToken[][]> {
  const instance = await ensure(lang)
  const lines = instance.codeToTokensBase(code, {
    lang,
    theme: 'css-variables',
    tokenizeMaxLineLength: maxLineLength,
  })
  return lines.map((line) => line.map(toToken))
}

const variable = /^var\(--shiki-token-([a-z-]+)\)$/

// toToken turns Shiki's colour, which the css-variables theme spells as
// var(--shiki-token-keyword), into the matching tok-keyword class from
// web/src/theme/shiki.css. The foreground colour needs no class.
function toToken(token: ThemedToken): HighlightedToken {
  const classes: string[] = []
  const match = token.color ? variable.exec(token.color) : null
  if (match) {
    classes.push(`tok-${match[1]}`)
  }
  const style = token.fontStyle ?? 0
  if (style & 1) {
    classes.push('tok-italic')
  }
  if (style & 2) {
    classes.push('tok-bold')
  }
  if (style & 4) {
    classes.push('tok-underline')
  }
  return { text: token.content, className: classes.join(' ') }
}
