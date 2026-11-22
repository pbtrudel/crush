import { useEffect, useRef } from 'preact/hooks';
import { deriveAgents } from '../../state/agents';
import { actions, state } from '../../state/store';
import { contextFill, needsAttention } from '../../state/reducer';
import { use3D } from '../../state/effects';
import { stageRect } from '../../state/layout';
import { AgentList } from './AgentList';
import { ConstellationSvg } from './ConstellationSvg';

export function Stage() {
  const s = state.value;
  const fill = contextFill(s);
  const attention = needsAttention(s);
  const threeD = use3D();
  const root = useRef<HTMLDivElement>(null);
  const nodes = deriveAgents(s.sessionId, s.team, s.runs)
    .map((a) => ({ id: a.sessionId, kind: a.kind as 'teammate' | 'subagent', status: a.status, name: a.name }));

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

  return (
    <div class="stage__inner" ref={root}>
      {!threeD && (
        <ConstellationSvg fill={fill} busy={s.busy} attention={attention} agents={nodes}
          selected={s.inspected?.sessionId ?? null} onSelect={(id) => void actions.inspect(id)} />
      )}
      <AgentList />
    </div>
  );
}
