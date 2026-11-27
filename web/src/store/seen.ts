const seenKey = 'crush.seen'

// readSeen returns the last-seen updated_at per session. It survives a Crush
// restart because the timestamps come from the workspace, not from this run.
export function readSeen(storage: Storage = localStorage): Record<string, number> {
  const raw = storage.getItem(seenKey)
  if (!raw) {
    return {}
  }
  try {
    const parsed = JSON.parse(raw) as Record<string, unknown>
    const out: Record<string, number> = {}
    for (const [sessionID, seq] of Object.entries(parsed)) {
      if (typeof seq === 'number') {
        out[sessionID] = seq
      }
    }
    return out
  } catch {
    return {}
  }
}

export function writeSeen(seen: Record<string, number>, storage: Storage = localStorage): void {
  storage.setItem(seenKey, JSON.stringify(seen))
}
