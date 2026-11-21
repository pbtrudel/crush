import { useEffect, useRef, useState } from 'preact/hooks';
import { state } from '../../state/store';
import { contextFill, needsAttention } from '../../state/reducer';
import { lowPower, motion } from '../../state/motion';
import type { SceneState } from '../../scene/constellation';
import { ConstellationSvg } from './ConstellationSvg';

function webglAvailable(): boolean {
  try {
    return !!document.createElement('canvas').getContext('webgl2');
  } catch {
    return false;
  }
}

type Scene = { update(s: SceneState): void; dispose(): void };

export function Stage() {
  const s = state.value;
  const fill = contextFill(s);
  const attention = needsAttention(s);
  // Probe once per mount: every probe creates a WebGL context, and browsers
  // cap how many may exist at the same time.
  const [webgl] = useState(webglAvailable);
  const [failed, setFailed] = useState(false);
  const use3D = webgl && !lowPower.value && !failed;
  const canvas = useRef<HTMLCanvasElement>(null);
  const scene = useRef<Scene | null>(null);
  const sceneState: SceneState = { busy: s.busy, contextFill: fill, attention, motion: motion.value };
  // The 3D chunk loads asynchronously, so remember the latest state rather
  // than the state captured when the mount effect ran.
  const latest = useRef(sceneState);
  latest.current = sceneState;

  // Leaving low power must retry the scene its own toggle disposed.
  useEffect(() => {
    setFailed(false);
  }, [lowPower.value]);

  useEffect(() => {
    if (!use3D || !canvas.current) return;
    let disposed = false;
    import('../../scene/constellation')
      .then(({ mountConstellation }) => {
        if (disposed || !canvas.current) return;
        scene.current = mountConstellation(canvas.current, latest.current);
      })
      .catch(() => {
        if (!disposed) setFailed(true);
      });
    return () => {
      disposed = true;
      scene.current?.dispose();
      scene.current = null;
    };
  }, [use3D]);

  useEffect(() => {
    scene.current?.update(latest.current);
  }, [s.busy, fill, attention, motion.value]);

  const status = attention ? 'Waiting for you' : s.busy ? 'Working' : 'Idle';
  return (
    <div class="stage__inner">
      {use3D
        ? <canvas ref={canvas} class="stage__canvas" aria-hidden="true" />
        : <ConstellationSvg fill={fill} busy={s.busy} attention={attention} />}
      <ul class="agent-list" aria-label="Agents">
        <li tabIndex={0} data-status={attention ? 'attention' : s.busy ? 'running' : 'idle'}>
          <span class="agent-list__glyph" aria-hidden="true">⬡</span>
          <span>Coder</span>
          <span class="hud-label">{status}</span>
        </li>
      </ul>
    </div>
  );
}
