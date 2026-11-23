import { useEffect, useRef } from 'preact/hooks';
import { state } from '../../state/store';
import { contextFill, needsAttention } from '../../state/reducer';
import { use3D } from '../../state/effects';
import { stageRect } from '../../state/layout';
import { ConstellationSvg } from './ConstellationSvg';

export function Stage() {
  const s = state.value;
  const fill = contextFill(s);
  const attention = needsAttention(s);
  const threeD = use3D();
  const root = useRef<HTMLDivElement>(null);

  // The projector is centered on this box. jsdom has no ResizeObserver, so
  // there the window listener alone publishes it.
  useEffect(() => {
    const el = root.current;
    if (!el) return;
    // Keep the window itself: Preact may run this cleanup after a test
    // environment has dropped the global.
    const target = window;
    const publish = () => { stageRect.value = el.getBoundingClientRect(); };
    publish();
    target.addEventListener('resize', publish);
    const observer = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(publish);
    observer?.observe(el);
    return () => {
      target.removeEventListener('resize', publish);
      observer?.disconnect();
      stageRect.value = null;
    };
  }, []);

  const status = attention ? 'Waiting for you' : s.busy ? 'Working' : 'Idle';
  return (
    <div class="stage__inner" ref={root}>
      {!threeD && <ConstellationSvg fill={fill} busy={s.busy} attention={attention} />}
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
