import { signal } from '@preact/signals';
import { readStored, writeStored } from './storage';

export const drawerOpen = signal(false);
export const selectedFile = signal<string | null>(null);

export type DiffMode = 'split' | 'unified';
const KEY_DIFF = 'crush-web-diff-mode';
export const diffMode = signal<DiffMode>(readStored(KEY_DIFF) === 'unified' ? 'unified' : 'split');
export function setDiffMode(m: DiffMode): void {
  diffMode.value = m;
  writeStored(KEY_DIFF, m);
}

// Below 1024px the side panels share one slot and these pick which one
// shows (spec 3.3).
export type PanelTab = 'log' | 'agent' | 'files';
export const panelTab = signal<PanelTab>('log');

// effectivePanelTab is the tab the narrow panel slot actually shows. Below
// 1024px the agent tab is the inspector's slot, so with no inspector to show
// the slot falls back to the log instead of going blank.
export function effectivePanelTab(tab: PanelTab, inspected: boolean): PanelTab {
  return tab === 'agent' && !inspected ? 'log' : tab;
}

export function toggleDrawer(): void {
  if (drawerOpen.value) {
    closeDrawer();
    return;
  }
  drawerOpen.value = true;
  panelTab.value = 'files';
}

// Closing gives the narrow panel slot back to the log. Above 1024px the
// tabs are hidden, so the bookkeeping has no visible effect there.
export function closeDrawer(): void {
  drawerOpen.value = false;
  if (panelTab.value === 'files') panelTab.value = 'log';
}

export function openFile(path: string): void {
  drawerOpen.value = true;
  selectedFile.value = path;
  panelTab.value = 'files';
}
