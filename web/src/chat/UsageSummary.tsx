import ContextRing from './ContextRing'
import { formatCost, formatTokens } from './format'
import styles from './Usage.module.css'

export interface Usage {
  // promptTokens and completionTokens are the last step's context, which
  // is what the window holds; cost is the session's running total.
  promptTokens: number
  completionTokens: number
  cost: number
  contextWindow: number
}

// UsageSummary is the header's line on the session: the context in use, its
// share of the window the current model offers, and what the session has
// cost. A field the gateway has not measured counts as nothing, and a
// window that is unknown or nonsensical drops the ring and the percentage
// rather than writing NaN or a share of an infinite window.
export default function UsageSummary({ usage }: { usage: Usage }) {
  const tokens = [usage.promptTokens, usage.completionTokens]
  const used = tokens.every((count) => Number.isFinite(count)) ? usage.promptTokens + usage.completionTokens : 0
  const cost = Number.isFinite(usage.cost) ? usage.cost : 0
  if (used === 0 && cost === 0) {
    return null
  }
  const contextWindow =
    Number.isFinite(usage.contextWindow) && usage.contextWindow > 0 ? usage.contextWindow : 0
  const fraction = contextWindow > 0 ? Math.min(1, Math.max(0, used / contextWindow)) : 0
  return (
    <div className={styles.summary} data-testid="usage-summary">
      {contextWindow > 0 ? <ContextRing fraction={fraction} /> : null}
      <span>{formatTokens(used)} tokens</span>
      {contextWindow > 0 ? (
        <span>
          {Math.round(fraction * 100)}% of {formatTokens(contextWindow)}
        </span>
      ) : null}
      <span>{formatCost(cost)}</span>
    </div>
  )
}
