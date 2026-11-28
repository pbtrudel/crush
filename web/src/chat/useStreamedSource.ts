import { useEffect, useRef, useState } from 'react'

// streamedSourceInterval is how often a streaming block hands its text to
// the renderer. Markdown parses the whole source again on every render, so a
// delta every few milliseconds costs quadratic work over a long reply: a
// measured 10 KiB reply streamed in 16-byte deltas parses for 7 s in total
// (0.5 ms for the first delta, 21 ms for the last). One update per pass caps
// that at about seven parses a second, and the last delta always lands.
export const streamedSourceInterval = 150

// useStreamedSource slows the source a streaming block renders. A settled
// block answers its source unchanged: only the block the model is still
// writing is held back.
export function useStreamedSource(
  source: string,
  streaming: boolean,
  interval = streamedSourceInterval,
): string {
  const [shown, setShown] = useState(source)
  const latest = useRef(source)
  const pending = useRef<number | null>(null)

  useEffect(() => {
    latest.current = source
    if (!streaming) {
      if (pending.current !== null) {
        window.clearTimeout(pending.current)
        pending.current = null
      }
      return
    }
    // A timer already pending reads latest.current when it fires, so the
    // deltas that arrive in between cost nothing.
    if (pending.current !== null) {
      return
    }
    pending.current = window.setTimeout(() => {
      pending.current = null
      setShown(latest.current)
    }, interval)
  }, [source, streaming, interval])

  // The timer is the hook's only side effect, so it goes with the component.
  useEffect(
    () => () => {
      if (pending.current !== null) {
        window.clearTimeout(pending.current)
      }
    },
    [],
  )

  return streaming ? shown : source
}
