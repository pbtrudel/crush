import { createElement, type ReactNode } from 'react'

export interface MathNode {
  tag: string
  attributes: Record<string, string>
  children: (MathNode | string)[]
}

// maxMathSource caps what one formula may cost to typeset. A formula past
// it is not a formula any more, and nesting-heavy TeX costs KaTeX more
// than linear time (deep groups exhaust the stack), so it shows its
// source instead.
export const maxMathSource = 8 * 1024

// The MathML Core elements KaTeX emits. Anything else is dropped with its
// subtree, so an element that could load or run something never survives.
const tags = new Set([
  'math', 'semantics', 'annotation', 'mrow', 'mi', 'mn', 'mo', 'ms', 'mtext', 'mspace',
  'msup', 'msub', 'msubsup', 'mfrac', 'msqrt', 'mroot', 'mover', 'munder', 'munderover',
  'mtable', 'mtr', 'mtd', 'mstyle', 'mpadded', 'mphantom', 'menclose', 'merror',
  'mmultiscripts', 'mprescripts', 'none',
])

// Presentation attributes only: no style (the CSP), no href (MathML Core
// links), no event handlers, no xmlns (React picks the namespace).
const attributes = new Set([
  'display', 'mathvariant', 'stretchy', 'fence', 'separator', 'lspace', 'rspace', 'width',
  'height', 'depth', 'voffset', 'minsize', 'maxsize', 'movablelimits', 'symmetric',
  'largeop', 'form', 'accent', 'accentunder', 'linethickness', 'displaystyle',
  'scriptlevel', 'columnalign', 'rowalign', 'columnspacing', 'rowspacing', 'columnlines',
  'rowlines', 'frame', 'framespacing', 'equalrows', 'equalcolumns', 'notation',
  'encoding', 'mathcolor', 'mathbackground', 'align', 'side',
])

function walk(element: Element): MathNode | null {
  const tag = element.localName
  if (!tags.has(tag)) {
    return null
  }
  const kept: Record<string, string> = {}
  for (const attribute of element.attributes) {
    if (attributes.has(attribute.name)) {
      kept[attribute.name] = attribute.value
    }
  }
  const children: (MathNode | string)[] = []
  for (const child of element.childNodes) {
    if (child.nodeType === Node.TEXT_NODE) {
      children.push(child.textContent ?? '')
    } else if (child.nodeType === Node.ELEMENT_NODE) {
      const node = walk(child as Element)
      if (node !== null) {
        children.push(node)
      }
    }
  }
  return { tag, attributes: kept, children }
}

// mathTree reads KaTeX's MathML string through the browser's own HTML
// parser, which applies the MathML foreign-content rules, and keeps only
// the allowlisted tree under the first <math>.
export function mathTree(html: string): MathNode | null {
  const parsed = new DOMParser().parseFromString(html, 'text/html')
  const math = parsed.querySelector('math')
  return math === null ? null : walk(math)
}

export function renderMathNode(node: MathNode | string, key: number): ReactNode {
  if (typeof node === 'string') {
    return node
  }
  return createElement(node.tag, { key, ...node.attributes }, ...node.children.map(renderMathNode))
}
