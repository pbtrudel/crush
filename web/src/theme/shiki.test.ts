import { describe, expect, it } from 'vitest'

import shiki from './shiki.css?raw'

// block returns the body of the declaration block that starts at selector,
// so a value is checked where the theme puts it: the light values on body
// and the dark overrides on html[data-ds-theme='dark'] body.
function block(selector: string): string {
  const start = shiki.indexOf(`${selector} {`)
  expect(start, `${selector} block`).toBeGreaterThanOrEqual(0)
  return shiki.slice(start, shiki.indexOf('}', start))
}

describe('shiki.css', () => {
  // Shiki's css-variables theme spells a diff as token-inserted,
  // token-deleted and token-changed; without these the classes carry no
  // colour and a diff fence renders as plain text.
  it('defines a colour for inserted, deleted and changed, light and dark', () => {
    const light = block('body')
    const dark = block("html[data-ds-theme='dark'] body")
    expect(light).toContain('--shiki-token-inserted: var(--dsw-alias-state-success')
    expect(light).toContain('--shiki-token-deleted: var(--dsw-alias-state-error')
    expect(light).toContain('--shiki-token-changed: var(--dsw-alias-state-warn')
    expect(dark).toContain('--shiki-token-inserted: var(--dsw-alias-state-success')
    expect(dark).toContain('--shiki-token-deleted: var(--dsw-alias-state-error')
    expect(dark).toContain('--shiki-token-changed: var(--dsw-alias-state-warn')
  })

  it('maps each diff class onto its variable', () => {
    for (const name of ['inserted', 'deleted', 'changed']) {
      expect(shiki).toContain(`.tok-${name} {`)
      expect(shiki).toContain(`color: var(--shiki-token-${name});`)
    }
  })
})
