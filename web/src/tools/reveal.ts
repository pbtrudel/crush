import { useCallback, useContext, useState, useSyncExternalStore } from 'react'

import { ToolEnvironmentContext } from './environment'

export interface Reveal {
  // all is true once the person asked for the rest of a clipped block.
  all: boolean
  reveal: () => void
}

// useReveal answers whether a clipped block shows everything, and the action
// that asks for the rest. A key names the block in the environment's revealed
// set, so the choice survives the row the virtualized log unmounts and mounts
// again; without one the block keeps the state itself.
export function useReveal(revealKey?: string): Reveal {
  const environment = useContext(ToolEnvironmentContext)
  const [localAll, setLocalAll] = useState(false)
  const readRevealed = useCallback(
    () => revealKey !== undefined && environment.revealed.has(revealKey),
    [environment, revealKey],
  )
  const revealed = useSyncExternalStore(environment.revealed.subscribe, readRevealed)
  return {
    all: revealKey === undefined ? localAll : revealed,
    reveal: () => {
      if (revealKey === undefined) {
        setLocalAll(true)
      } else {
        environment.reveal(revealKey)
      }
    },
  }
}
