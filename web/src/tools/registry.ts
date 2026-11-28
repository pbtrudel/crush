import type { ToolView } from './view'
import { bashView, jobKillView, jobOutputView } from './views/BashView'
import { downloadView, fetchView, webFetchView, webSearchView } from './views/FetchView'
import { editView, multiEditView, readView, writeView } from './views/FileViews'
import { genericView } from './views/GenericView'
import { mcpView } from './views/McpView'
import { globView, grepView, lsView, sourcegraphView } from './views/SearchView'
import { todosView } from './views/TodosView'

// views maps a tool name onto its view. A built-in tool whose name wears the
// MCP prefix keeps an entry of its own, so the prefix rule below never takes
// it for a server call.
const views = new Map<string, ToolView>([
  ['bash', bashView],
  ['job_output', jobOutputView],
  ['job_kill', jobKillView],
  ['view', readView],
  ['read', readView],
  ['edit', editView],
  ['multiedit', multiEditView],
  ['write', writeView],
  ['grep', grepView],
  ['glob', globView],
  ['ls', lsView],
  ['sourcegraph', sourcegraphView],
  ['fetch', fetchView],
  ['web_fetch', webFetchView],
  ['web_search', webSearchView],
  ['download', downloadView],
  ['todos', todosView],
  ['mcp_servers', genericView],
])

// toolView answers the view for a tool name: its own entry, else the MCP
// view for a server call (mcp_<server>_<tool>), else the generic view.
export function toolView(name: string): ToolView {
  return views.get(name) ?? (name.startsWith('mcp_') ? mcpView : genericView)
}
