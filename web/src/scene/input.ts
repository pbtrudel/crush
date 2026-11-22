import type { SceneInput } from './mount';

// sameSceneInput tells a real state change from a re-render that publishes
// the same scene state. Type-only above, so the eager scene layer can use
// this without pulling three into its chunk.
export function sameSceneInput(a: SceneInput, b: SceneInput): boolean {
  return (
    a.mode === b.mode &&
    a.fill === b.fill &&
    a.motion === b.motion &&
    a.booting === b.booting &&
    a.effects.bloom === b.effects.bloom &&
    a.effects.beams === b.effects.beams &&
    a.effects.particleShare === b.effects.particleShare &&
    a.effects.cinematic === b.effects.cinematic &&
    sameFocus(a.focus, b.focus) &&
    a.selected === b.selected &&
    sameAgentList(a.agents, b.agents)
  );
}

// sameAgentList compares the roster a frame draws: an agent that kept its
// id, kind and status needs no new node.
export function sameAgentList(a: SceneInput['agents'], b: SceneInput['agents']): boolean {
  return (
    a.length === b.length &&
    a.every((x, i) => x.id === b[i].id && x.kind === b[i].kind && x.status === b[i].status)
  );
}

function sameFocus(a: SceneInput['focus'], b: SceneInput['focus']): boolean {
  if (a === b) return true;
  if (!a || !b) return false;
  return a.x === b.x && a.y === b.y && a.width === b.width && a.height === b.height;
}
