import { drawerOpen, effectivePanelTab, panelTab, type PanelTab } from '../state/drawer';
import { state } from '../state/store';

export function PanelTabs({ inert = false }: { inert?: boolean }) {
  const agentOpen = state.value.inspected !== null;
  // The pressed tab follows what the slot shows, not the stored choice: a
  // stored agent tab with nothing to inspect shows the log.
  const tab = effectivePanelTab(panelTab.value, agentOpen);
  const pick = (t: PanelTab) => {
    panelTab.value = t;
    // The drawer is the files slot, so picking any other tab leaves it.
    drawerOpen.value = t === 'files';
  };
  const button = (t: PanelTab, label: string, disabled = false) => (
    <button type="button" class="hud__mode-btn" aria-pressed={tab === t} disabled={disabled} onClick={() => pick(t)}>{label}</button>
  );
  return (
    <div class="panel-tabs" role="group" aria-label="Panel" inert={inert}>
      {button('log', 'Log')}
      {button('agent', 'Agent', !agentOpen)}
      {button('files', 'Files')}
    </div>
  );
}
