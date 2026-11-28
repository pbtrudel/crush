import type { AlignType, Definition, Nodes, Root, Table } from 'mdast'
import { Fragment, createElement, memo, useMemo, type ReactNode } from 'react'

import CodeBlock from './CodeBlock'
import MathFormula from './Math'
import { safeHref } from './links'
import { parseMarkdown } from './parse'
import styles from './Markdown.module.css'

export interface MarkdownProps {
  source: string
}

interface RenderContext {
  definitions: Map<string, Definition>
}

// Markdown renders assistant text. The tree is walked node by node into
// React elements: there is no HTML string anywhere, so raw HTML in the
// source can only ever be text.
function Markdown({ source }: MarkdownProps) {
  const tree = useMemo(() => parseMarkdown(source), [source])
  const context = useMemo(() => ({ definitions: collectDefinitions(tree) }), [tree])
  return <div className={styles.markdown}>{children(tree.children, context)}</div>
}

export default memo(Markdown)

function collectDefinitions(root: Root): Map<string, Definition> {
  const out = new Map<string, Definition>()
  const visit = (node: Nodes) => {
    if (node.type === 'definition') {
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
