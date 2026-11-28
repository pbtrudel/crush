// followDistance is how close to the end the log counts as "at the end". A
// few pixels of slack keep a fractional device pixel from unpinning it.
export const followDistance = 24

export function isAtEnd(metrics: {
  scrollTop: number
  scrollHeight: number
  clientHeight: number
}): boolean {
  return metrics.scrollHeight - metrics.scrollTop - metrics.clientHeight <= followDistance
}

// prependedCount is how many rows went in front of the old first row. An
// append, a stream, a reset to another list or a first render is zero.
export function prependedCount(before: readonly string[], after: readonly string[]): number {
  if (before.length === 0) {
    return 0
  }
  const index = after.indexOf(before[0])
  return index > 0 ? index : 0
}

// anchoredScrollTop keeps an anchor row where it was on screen: the view
// moves by exactly as much as the row moved down the list.
export function anchoredScrollTop(
  previousTop: number,
  previousStart: number,
  nextStart: number,
): number {
  return previousTop + (nextStart - previousStart)
}
