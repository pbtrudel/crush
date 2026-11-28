// formatDuration writes a span of whole seconds the way the TUI does.
export function formatDuration(seconds: number): string {
  const whole = Math.max(0, Math.round(seconds))
  if (whole < 60) {
    return `${whole}s`
  }
  return `${Math.floor(whole / 60)}m ${whole % 60}s`
}

function oneDecimal(value: number): string {
  return value.toFixed(1).replace(/\.0$/, '')
}

// formatTokens writes a token count in K and M with one decimal.
export function formatTokens(tokens: number): string {
  if (tokens < 1000) {
    return String(tokens)
  }
  if (tokens < 1_000_000) {
    return `${oneDecimal(tokens / 1000)}K`
  }
  return `${oneDecimal(tokens / 1_000_000)}M`
}
