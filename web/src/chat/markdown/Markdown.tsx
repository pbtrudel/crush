import type { AlignType, Definition, Nodes, Root, Table } from 'mdast'
import { Fragment, createElement, memo, useMemo, type ReactNode } from 'react'

import CodeBlock from './CodeBlock'
import MarkdownBoundary from './MarkdownBoundary'
import MathFormula from './Math'
import PlainText from './PlainText'
import { safeHref } from './links'
import { maxMarkdownDepth, parseMarkdown, treeDepth } from './parse'
import styles from './Markdown.module.css'

export interface MarkdownProps {
  source: string
}

// maxMarkdownSource caps what one message may cost to parse. Past it the
// source renders as text: a coding chat has no legitimate reply that long,
// and the furthest tails of adversarial input make a parser quadratic.
export const maxMarkdownSource = 32 * 1024

// deepPrefix finds a marker run that would nest past the depth cap: 65 or
// more quote, bullet or ordered-list markers at the start of a line. The
// parser crawls such a run long before a tree exists to measure it, so the
// source renders as text instead of being parsed at all.
const deepPrefix = /^[ \t]*(?:>[ \t]*|(?:[-+*]|\d{1,9}[.)])[ \t]+){65,}/m

interface RenderContext {
  definitions: Map<string, Definition>
}

// Markdown renders assistant text. The tree is walked node by node into
// React elements: there is no HTML string anywhere, so raw HTML in the
// source can only ever be text.
function Markdown({ source }: MarkdownProps) {
  const tree = useMemo(() => parseBounded(source), [source])
  const definitions = useMemo(() => (tree === null ? null : collectDefinitions(tree)), [tree])
  if (tree === null || definitions === null) {
    return <PlainText source={source} />
  }
  return (
    <MarkdownBoundary source={source}>
      <MarkdownTree tree={tree} definitions={definitions} />
    </MarkdownBoundary>
  )
}

export default memo(Markdown)

interface MarkdownTreeProps {
  tree: Root
  definitions: Map<string, Definition>
}

// MarkdownTree walks the parsed tree. It is a component of its own so the
// boundary above it can catch a renderer that throws: a walk run here is
// inside the boundary's subtree, unlike one run in Markdown's own render.
function MarkdownTree({ tree, definitions }: MarkdownTreeProps) {
  return <div className={styles.markdown}>{children(tree.children, { definitions })}</div>
}

// parseBounded answers the tree to render, or null when the source must
// show as text: too long to parse, nested past the depth cap before any
// tree exists to measure, too deep once parsed, or a parser that threw.
function parseBounded(source: string): Root | null {
  if (source.length > maxMarkdownSource || deepPrefix.test(source)) {
    return null
  }
  try {
    const tree = parseMarkdown(source)
    return treeDepth(tree) > maxMarkdownDepth ? null : tree
  } catch {
    return null
  }
}

function collectDefinitions(root: Root): Map<string, Definition> {
  const out = new Map<string, Definition>()
  const visit = (node: Nodes) => {
    // The first definition wins, as in CommonMark.
    if (node.type === 'definition' && !out.has(node.identifier)) {
      out.set(node.identifier, node)
    }
    if ('children' in node) {
      for (const child of node.children) {
        visit(child)
      }
    }
  }
  visit(root)
  return out
}

function children(nodes: Nodes[], context: RenderContext): ReactNode[] {
  return nodes.map((node, index) => <Fragment key={index}>{render(node, context)}</Fragment>)
}

function link(url: string, content: ReactNode): ReactNode {
  const href = safeHref(url)
  if (href === null) {
    return <span className={styles.inertLink}>{content}</span>
  }
  return (
    <a href={href} target="_blank" rel="noopener noreferrer">
      {content}
    </a>
  )
}

const alignClass: Record<NonNullable<AlignType>, string> = {
  left: styles.alignLeft,
  center: styles.alignCenter,
  right: styles.alignRight,
}

function table(node: Table, context: RenderContext): ReactNode {
  const [head, ...body] = node.children
  const cell = (tag: 'th' | 'td', column: number, content: Nodes[]) => {
    const align = node.align?.[column]
    return createElement(
      tag,
      { key: column, className: align ? alignClass[align] : undefined },
      children(content, context),
    )
  }
  return (
    <div className={styles.tableScroll}>
      <table>
        {head ? (
          <thead>
            <tr>{head.children.map((entry, column) => cell('th', column, entry.children))}</tr>
          </thead>
        ) : null}
        <tbody>
          {body.map((row, index) => (
            <tr key={index}>{row.children.map((entry, column) => cell('td', column, entry.children))}</tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function render(node: Nodes, context: RenderContext): ReactNode {
  switch (node.type) {
    case 'root':
      return children(node.children, context)
    case 'paragraph':
      return <p>{children(node.children, context)}</p>
    case 'heading':
      // The page title is the h1 and the conversation sits under it, so a
      // message's headings start one level down.
      return createElement(`h${Math.min(node.depth + 1, 6)}`, null, children(node.children, context))
    case 'text':
      return node.value
    case 'emphasis':
      return <em>{children(node.children, context)}</em>
    case 'strong':
      return <strong>{children(node.children, context)}</strong>
    case 'delete':
      return <del>{children(node.children, context)}</del>
    case 'inlineCode':
      return <code className={styles.inlineCode}>{node.value}</code>
    case 'code':
      if (node.lang === 'math') {
        return <MathFormula tex={node.value} display />
      }
      return <CodeBlock code={node.value} language={node.lang ?? null} />
    case 'math':
      return <MathFormula tex={node.value} display />
    case 'inlineMath':
      return <MathFormula tex={node.value} display={false} />
    case 'blockquote':
      return <blockquote>{children(node.children, context)}</blockquote>
    case 'list':
      return node.ordered ? (
        <ol start={node.start ?? undefined}>{children(node.children, context)}</ol>
      ) : (
        <ul>{children(node.children, context)}</ul>
      )
    case 'listItem':
      if (node.checked === true || node.checked === false) {
        return (
          <li className={styles.task}>
            <input type="checkbox" checked={node.checked} disabled readOnly aria-label={node.checked ? 'Done' : 'Not done'} />
            {children(node.children, context)}
          </li>
        )
      }
      return <li>{children(node.children, context)}</li>
    case 'thematicBreak':
      return <hr />
    case 'break':
      return <br />
    case 'link':
      return link(node.url, children(node.children, context))
    case 'linkReference': {
      const definition = context.definitions.get(node.identifier)
      const content = children(node.children, context)
      return definition ? link(definition.url, content) : <span>{content}</span>
    }
    case 'image':
      // The CSP loads images from the gateway only, and a remote image is a
      // tracking beacon anyway: it becomes a link the person can choose.
      return link(node.url, <span className={styles.imageLink}>Image: {node.alt || node.url}</span>)
    case 'imageReference': {
      const definition = context.definitions.get(node.identifier)
      const label = <span className={styles.imageLink}>Image: {node.alt || node.identifier}</span>
      return definition ? link(definition.url, label) : label
    }
    case 'definition':
      return null
    case 'footnoteReference':
      return <sup className={styles.footnoteRef}>[{node.label ?? node.identifier}]</sup>
    case 'footnoteDefinition':
      return (
        <div className={styles.footnote}>
          <sup>{node.label ?? node.identifier}</sup> {children(node.children, context)}
        </div>
      )
    case 'table':
      return table(node, context)
    case 'html':
      // Raw HTML is the model's text, shown as it was written.
      return <span className={styles.rawHtml}>{node.value}</span>
    default:
      return null
  }
}
