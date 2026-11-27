import { use3D } from '../state/effects';

export function BootSequence({ done }: { done: boolean }) {
  // Without a scene the 2D view still needs a loading state, so it keeps the
  // CSS rings; with one, the scene itself draws the power-up.
  const scene = use3D();
  return (
    <div class="boot holo" data-done={done} aria-hidden={done}>
      {scene ? null : (
        <div class="boot__rings" aria-hidden="true">
          <span />
          <span />
          <span />
        </div>
      )}
      <p class="hud-label">{done ? 'Link established' : 'Linking to the crush session'}</p>
    </div>
  );
}
