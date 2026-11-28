import type { ToolView } from './view'
import { bashView, jobKillView, jobOutputView } from './views/BashView'
import { editView, multiEditView, readView, writeView } from './views/FileViews'
import { genericView } from './views/GenericView'

// views maps a tool name onto its view. The later M2 tasks add entries.
const views = new Map<string, ToolView>([
  ['bash', bashView],
  ['job_output', jobOutputView],
  ['job_kill', jobKillView],
  ['view', readView],
  ['read', readView],
  ['edit', editView],
  ['multiedit', multiEditView],
  ['write', writeView],
])

export function toolView(name: string): ToolView {
  return views.get(name) ?? genericView
}
