import { Component, type ReactNode } from 'react'

import PlainText from './PlainText'

export interface MarkdownBoundaryProps {
  source: string
  children: ReactNode
}

interface MarkdownBoundaryState {
  failed: boolean
}

// MarkdownBoundary keeps one unrenderable message from unmounting the
// conversation: it shows the source as text instead, and the next source
// gets a fresh attempt.
export default class MarkdownBoundary extends Component<MarkdownBoundaryProps, MarkdownBoundaryState> {
  state: MarkdownBoundaryState = { failed: false }

  static getDerivedStateFromError(): MarkdownBoundaryState {
    return { failed: true }
  }

  componentDidUpdate(prevProps: MarkdownBoundaryProps) {
    if (this.state.failed && prevProps.source !== this.props.source) {
      this.setState({ failed: false })
    }
  }

  render() {
    if (this.state.failed) {
      return <PlainText source={this.props.source} />
    }
    return this.props.children
  }
}
