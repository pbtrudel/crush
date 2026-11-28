import katex from 'katex'
import { describe, expect, it } from 'vitest'

import { mathTree } from './mathml'

describe('mathTree', () => {
  it('keeps the MathML KaTeX writes', () => {
    const tree = mathTree(katex.renderToString('x^2', { output: 'mathml', throwOnError: true }))
    expect(tree?.tag).toBe('math')
    expect(JSON.stringify(tree)).toContain('"msup"')
  })

  it('drops style, href and event attributes, and foreign elements', () => {
    const tree = mathTree(
      '<span class="katex"><math><mi style="color:red" href="javascript:alert(1)" onclick="x()" mathvariant="bold">a</mi><img src="x"><script>alert(1)</script></math></span>',
    )
    expect(tree).toEqual({
      tag: 'math',
      attributes: {},
      children: [{ tag: 'mi', attributes: { mathvariant: 'bold' }, children: ['a'] }],
    })
  })

  it('answers null when there is no math element', () => {
    expect(mathTree('<span>no math</span>')).toBeNull()
  })

  it('keeps no style, href, event or namespace attribute of real output', () => {
    const tree = mathTree(
      katex.renderToString('\\begin{matrix}a&b\\\\c&d\\end{matrix}', {
        output: 'mathml',
        displayMode: true,
        throwOnError: true,
        trust: false,
        strict: 'ignore',
      }),
    )
    const html = JSON.stringify(tree)
    expect(tree?.tag).toBe('math')
    expect(tree?.attributes.display).toBe('block')
    expect(tree?.attributes.xmlns).toBeUndefined()
    expect(html).not.toContain('"style"')
    expect(html).not.toContain('"href"')
    expect(html).not.toContain('"on')
  })
})
