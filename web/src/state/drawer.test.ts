import { beforeEach, describe, expect, it, vi } from 'vitest';
import { readStored } from './storage';
import { closeDrawer, diffMode, drawerOpen, openFile, panelTab, selectedFile, setDiffMode, toggleDrawer } from './drawer';

describe('the drawer state', () => {
  beforeEach(() => {
    drawerOpen.value = false;
    selectedFile.value = null;
    panelTab.value = 'log';
    diffMode.value = 'split';
  });

  it('persists the diff mode under its storage key', () => {
    setDiffMode('unified');
    expect(diffMode.value).toBe('unified');
    expect(readStored('crush-web-diff-mode')).toBe('unified');
    setDiffMode('split');
    expect(readStored('crush-web-diff-mode')).toBe('split');
  });

  it('reads the stored diff mode when the module loads', async () => {
    localStorage.setItem('crush-web-diff-mode', 'unified');
    vi.resetModules();
    const fresh = await import('./drawer');
    expect(fresh.diffMode.value).toBe('unified');
    localStorage.removeItem('crush-web-diff-mode');
  });

  it('opens the drawer on the files tab and gives it back on close', () => {
    toggleDrawer();
    expect(drawerOpen.value).toBe(true);
    expect(panelTab.value).toBe('files');
    toggleDrawer();
    expect(drawerOpen.value).toBe(false);
    expect(panelTab.value).toBe('log');
  });

  it('opens a file in the drawer and closes back to the log', () => {
    openFile('/repo/a.go');
    expect(drawerOpen.value).toBe(true);
    expect(panelTab.value).toBe('files');
    expect(selectedFile.value).toBe('/repo/a.go');
    closeDrawer();
    expect(drawerOpen.value).toBe(false);
    expect(panelTab.value).toBe('log');
  });
});
