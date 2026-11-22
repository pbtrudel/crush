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
    sameFocus(a.focus, b.focus)
  );
}

function sameFocus(a: SceneInput['focus'], b: SceneInput['focus']): boolean {
  if (a === b) return true;
  if (!a || !b) return false;
  return a.x === b.x && a.y === b.y && a.width === b.width && a.height === b.height;
}
