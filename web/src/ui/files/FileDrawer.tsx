import { closeDrawer, drawerOpen, selectedFile } from '../../state/drawer';
import { state } from '../../state/store';
import { changeBlocks, fileGroups } from './fileGroups';
import { DiffPanel } from './DiffPanel';

export function FileDrawer() {
  const s = state.value;
  const open = drawerOpen.value;
  const selected = selectedFile.value;
  const groups = fileGroups(s.files, s.workingDir);
  return (
    <aside id="file-drawer" class="panel panel--drawer glass scanlines" aria-labelledby="drawer-title" data-open={open} inert={!open}>
      <header class="drawer__head">
        <h2 id="drawer-title">Edited files <span class="hud-label">{s.files.length}</span></h2>
        <button type="button" class="btn btn--ghost" aria-label="Close edited files" onClick={closeDrawer}>×</button>
      </header>
      {s.files.length === 0 ? (
        <p class="drawer__empty">No edited files yet. Edits appear here as agents write.</p>
      ) : (
        <nav class="drawer__list" aria-label="Edited files">
          {groups.map((g) => (
            <section key={g.dir} class="drawer__group">
              <h3 class="hud-label">{g.dir || 'Project root'}</h3>
              <ul>
                {g.files.map((f) => (
                  <li key={f.path}>
                    <button type="button" class="file-row" aria-pressed={selected === f.path} title={f.rel} onClick={() => { selectedFile.value = f.path; }}>
                      <span class="file-row__name">{f.name}</span>
                      <span class="file-row__bars" aria-hidden="true">
                        {changeBlocks(f.additions, f.removals).map((b, i) => <span key={i} class="file-row__bar" data-kind={b} />)}
                      </span>
                      <span class="file-row__stat">
                        {f.stats_unknown ? (
                          <span class="file-row__unknown">changes unknown</span>
                        ) : (
                          <>
                            <span class="file-row__add">+{f.additions}</span> <span class="file-row__rem">−{f.removals}</span>
                          </>
                        )}
                      </span>
                      {f.diagnostics.error > 0 && (
                        <span class="file-row__diag" data-level="error"><span aria-hidden="true">✕ </span>{f.diagnostics.error}<span class="visually-hidden"> {f.diagnostics.error === 1 ? 'error' : 'errors'}</span></span>
                      )}
                      {f.diagnostics.warning > 0 && (
                        <span class="file-row__diag" data-level="warning"><span aria-hidden="true">▲ </span>{f.diagnostics.warning}<span class="visually-hidden"> {f.diagnostics.warning === 1 ? 'warning' : 'warnings'}</span></span>
                      )}
                    </button>
                  </li>
                ))}
              </ul>
            </section>
          ))}
        </nav>
      )}
      <div class="drawer__diff">{selected && <DiffPanel key={selected} path={selected} />}</div>
    </aside>
  );
}
