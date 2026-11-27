export const sidebarMinWidth = 264
export const sidebarMaxWidth = 420
export const sidebarDefaultWidth = 280
export const sidebarRailWidth = 56

// clampSidebarWidth keeps a drag inside the DSH range and whole pixels, so
// the grid never lands on a half-pixel column.
export function clampSidebarWidth(px: number): number {
  return Math.min(sidebarMaxWidth, Math.max(sidebarMinWidth, Math.round(px)))
}
