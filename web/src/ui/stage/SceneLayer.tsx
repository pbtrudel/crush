import { useEffect, useRef } from 'preact/hooks';
import type { SceneAgent } from '../../scene/agents';
import { sameSceneInput } from '../../scene/input';
import type { SceneHandle, SceneInput } from '../../scene/mount';
import { deriveAgents } from '../../state/agents';
import { cinematic, cinematicAllowed, setCinematic } from '../../state/cinematic';
import { effectFlags, effectsLevel, sceneFailed, use3D } from '../../state/effects';
import { publishAgentScreens, stageRect } from '../../state/layout';
import { coreMode } from '../../state/levels';
import { lowPower, motion } from '../../state/motion';
import { contextFill } from '../../state/reducer';
import { actions, state } from '../../state/store';
import { telemetry } from '../../state/telemetry';
import { toolCategory } from '../../state/toolCategory';

interface ElementRef {
  readonly current: HTMLElement | null;
}

// The log's wrappers live in the App, which also renders this layer, so the
// cinematic flight receives them rather than looking them up by class.
export interface SceneLayerProps {
  view?: ElementRef;
  camera?: ElementRef;
  panels?: ElementRef[];
}

export function SceneLayer({ view, camera, panels }: SceneLayerProps = {}) {
  const s = state.value;
  const enabled = use3D();
  const tools = telemetry.value.tools;
  const input: SceneInput = {
    mode: coreMode(s),
    fill: contextFill(s),
    motion: motion.value,
    effects: effectFlags(effectsLevel.value, lowPower.value),
    booting: s.conn === 'booting',
    focus: stageRect.value,
    agents: deriveAgents(s.sessionId, s.team, s.runs).map((a) => ({ id: a.sessionId, kind: a.kind as SceneAgent['kind'], status: a.status })),
    selected: s.inspected?.sessionId ?? null,
  };
  const canvas = useRef<HTMLCanvasElement>(null);
  const scene = useRef<SceneHandle | null>(null);
  const lastBurst = useRef<string | undefined>(undefined);
  // The call id each agent was last seen running, so a burst fires once per
  // call rather than on every frame that repeats it.
  const lastCalls = useRef<Record<string, string>>({});
  // The 3D chunk loads asynchronously, so remember the latest state rather
  // than the state captured when the mount effect ran.
  const latest = useRef(input);
  latest.current = input;
  // Every state change re-renders this layer, and most of them leave the
  // scene alone. Remember what the scene was last told so a re-render does
  // not wake it.
  const applied = useRef<SceneInput | null>(null);

  useEffect(() => {
    if (!enabled || !canvas.current) return;
    let disposed = false;
    import('../../scene/mount')
      .then(({ mountScene }) => {
        if (disposed || !canvas.current) return;
        const handle = mountScene(canvas.current, latest.current, {
          onStepDown: (level) => actions.effectsReduced(level),
          onFail: () => {
            sceneFailed.value = true;
          },
          onAgentScreens: publishAgentScreens,
        });
        scene.current = handle;
        applied.current = latest.current;
        // Seed from the window so the 15 minutes it holds never replay as
        // a storm of rings.
        lastBurst.current = telemetry.peek().tools.at(-1)?.id;
        lastCalls.current = Object.fromEntries(Object.entries(state.peek().activity).map(([sid, c]) => [sid, c.callId]));
        handle.update(latest.current);
      })
      .catch(() => {
        if (!disposed) sceneFailed.value = true;
      });
    return () => {
      disposed = true;
      scene.current?.dispose();
      scene.current = null;
    };
  }, [enabled]);

  useEffect(() => {
    const handle = scene.current;
    if (!handle) return;
    if (applied.current && sameSceneInput(applied.current, latest.current)) return;
    applied.current = latest.current;
    handle.update(latest.current);
  }, [s, motion.value, effectsLevel.value, lowPower.value, stageRect.value]);

  // The flight lives in its own chunk, so only a visitor who enters
  // cinematic mode downloads it.
  useEffect(() => {
    if (!cinematic.value) return;
    const handle = scene.current;
    const viewEl = view?.current;
    const cameraEl = camera?.current;
    const panelEls = (panels ?? []).map((p) => p.current).filter((el): el is HTMLElement => el !== null);
    // Without a scene, its wrappers or the permission to fly there is
    // nothing to start, so the mode fails closed instead of stranding the
    // layout.
    if (!cinematicAllowed() || !handle || !viewEl || !cameraEl || !panelEls.length) {
      setCinematic(false);
      return;
    }
    let cancelled = false;
    let flight: { stop(): Promise<void> } | null = null;
    void import('../../scene/cinematic')
      .then(({ startCinematic }) => {
        if (cancelled) return;
        flight = startCinematic(handle, { view: viewEl, camera: cameraEl, panels: panelEls }, motion.value);
      })
      .catch(() => {
        if (!cancelled) setCinematic(false);
      });
    return () => {
      cancelled = true;
      void flight?.stop();
    };
  }, [cinematic.value, enabled]);

  useEffect(() => {
    const handle = scene.current;
    if (!handle) return;
    const last = lastBurst.current;
    // A pruned id means everything left in the window is newer than it.
    const start = last === undefined ? 0 : tools.findIndex((e) => e.id === last) + 1;
    for (const event of tools.slice(start)) handle.burst(event.category);
    if (tools.length > start) lastBurst.current = tools[tools.length - 1].id;
  }, [tools]);

  // A child agent's call is not in the main transcript, so its ring is
  // spawned at its node from the activity map instead.
  useEffect(() => {
    const handle = scene.current;
    const seen = lastCalls.current;
    lastCalls.current = Object.fromEntries(Object.entries(s.activity).map(([sid, c]) => [sid, c.callId]));
    if (!handle) return;
    for (const [sid, call] of Object.entries(s.activity)) {
      if (seen[sid] !== call.callId) handle.burst(toolCategory(call.tool), sid);
    }
  }, [s.activity]);

  if (!enabled) return null;
  return <canvas ref={canvas} class="scene-layer__canvas" aria-hidden="true" />;
}
