import type { ToolView } from './view'
import { genericView } from './views/GenericView'

// views maps a tool name onto its view. The later M2 tasks add entries.
const views = new Map<string, ToolView>([])

export function toolView(name: string): ToolView {
  return views.get(name) ?? genericView
}
