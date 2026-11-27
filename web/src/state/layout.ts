import { signal } from '@preact/signals';

// The stage box in viewport coordinates. Stage publishes it and the scene
// layer reads it, so the projector can sit in the empty center column.
export const stageRect = signal<DOMRectReadOnly | null>(null);

// Agent node positions in viewport pixels, written by the scene after a
// render. Tags apply them through the CSSOM, so a moving node never
// re-renders the page.
export interface AgentScreen { id: string; x: number; y: number; visible: boolean }

let lastScreens: AgentScreen[] = [];
let screenSink: ((s: AgentScreen[]) => void) | null = null;

export function publishAgentScreens(screens: AgentScreen[]): void {
  lastScreens = screens;
  screenSink?.(screens);
}

export function subscribeAgentScreens(fn: (s: AgentScreen[]) => void): () => void {
  screenSink = fn;
  fn(lastScreens);
  return () => {
    if (screenSink === fn) screenSink = null;
  };
}

export function currentAgentScreens(): AgentScreen[] {
  return lastScreens;
}
