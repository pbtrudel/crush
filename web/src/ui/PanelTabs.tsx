import { drawerOpen, panelTab, type PanelTab } from '../state/drawer';
import { state } from '../state/store';

export function PanelTabs({ inert = false }: { inert?: boolean }) {
  const agentOpen = state.value.inspected !== null;
  const pick = (t: PanelTab) => {
    panelTab.value = t;
    // The drawer is the files slot, so picking any other tab leaves it.
    drawerOpen.value = t === 'files';
  };
  const tab = (t: PanelTab, label: string, disabled = false) => (
    <button type="button" class="hud__mode-btn" aria-pressed={panelTab.value === t} disabled={disabled} onClick={() => pick(t)}>{label}</button>
  );
  return (
    <div class="panel-tabs" role="group" aria-label="Panel" inert={inert}>
      {tab('log', 'Log')}
      {tab('agent', 'Agent', !agentOpen)}
      {tab('files', 'Files')}
    </div>
  );
}
