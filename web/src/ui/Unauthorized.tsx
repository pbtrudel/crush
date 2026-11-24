export function Unauthorized() {
  return (
    <main class="unauthorized">
      <h1 class="hud__title">This tab is not linked to a crush session</h1>
      <p>This tab has no link to a crush session: the run has ended, or the tab was opened without its link. In the crush terminal, run <code>/web</code> to open a working link. Each new tab needs its own link from <code>/web</code>.</p>
    </main>
  );
}
