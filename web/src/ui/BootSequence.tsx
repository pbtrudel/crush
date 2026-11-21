export function BootSequence({ done }: { done: boolean }) {
  return (
    <div class="boot" data-done={done} aria-hidden={done}>
      <div class="boot__rings" aria-hidden="true"><span /><span /><span /></div>
      <p class="hud-label" role="status">{done ? 'Link established' : 'Linking to the crush session'}</p>
    </div>
  );
}
