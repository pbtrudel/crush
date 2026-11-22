import { useEffect, useRef } from 'preact/hooks';
import { state } from '../../state/store';
import { contextFill, needsAttention } from '../../state/reducer';
import { motion } from '../../state/motion';
import { sceneFailed, use3D } from '../../state/effects';
import { stageRect } from '../../state/layout';
import type { SceneState } from '../../scene/constellation';

type Scene = { update(s: SceneState): void; dispose(): void };

export function SceneLayer() {
  const s = state.value;
  const fill = contextFill(s);
  const attention = needsAttention(s);
  const enabled = use3D();
  // Task 4 centers the projector on the stage box; reading it here keeps the
  // layer subscribed so a layout change re-runs the update below.
  const rect = stageRect.value;
  const canvas = useRef<HTMLCanvasElement>(null);
  const scene = useRef<Scene | null>(null);
  const sceneState: SceneState = { busy: s.busy, contextFill: fill, attention, motion: motion.value };
  // The 3D chunk loads asynchronously, so remember the latest state rather
  // than the state captured when the mount effect ran.
  const latest = useRef(sceneState);
  latest.current = sceneState;

  useEffect(() => {
    if (!enabled || !canvas.current) return;
    let disposed = false;
    import('../../scene/constellation')
      .then(({ mountConstellation }) => {
        if (disposed || !canvas.current) return;
        scene.current = mountConstellation(canvas.current, latest.current);
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
    scene.current?.update(latest.current);
  }, [s.busy, fill, attention, motion.value, rect]);

  if (!enabled) return null;
  return <canvas ref={canvas} class="scene-layer__canvas" aria-hidden="true" />;
}
