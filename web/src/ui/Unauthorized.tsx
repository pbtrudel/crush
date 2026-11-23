export function Unauthorized() {
  return (
    <main class="unauthorized">
      <h1 class="hud__title">This tab is not linked to a crush session</h1>
      <p>This link belongs to a crush run that has ended or never started. In the crush terminal, run <code>/web</code> to open a working link.</p>
    </main>
  );
}
