import type { ToolView } from './view'
import { bashView, jobKillView, jobOutputView } from './views/BashView'
import { genericView } from './views/GenericView'

// views maps a tool name onto its view. The later M2 tasks add entries.
const views = new Map<string, ToolView>([
  ['bash', bashView],
  ['job_output', jobOutputView],
  ['job_kill', jobKillView],
])

export function toolView(name: string): ToolView {
  return views.get(name) ?? genericView
}
