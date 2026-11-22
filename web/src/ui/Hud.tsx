import { state, actions } from '../state/store';
import { contextFill } from '../state/reducer';
import type { PermissionMode } from '../api/types';
import { MotionMenu } from './MotionMenu';

const fmtTokens = (n: number) => (n >= 1000 ? `${(n / 1000).toFixed(1)}k` : String(n));

export function Hud() {
  const s = state.value;
  const fill = contextFill(s);
  const tokens = s.session ? s.session.prompt_tokens + s.session.completion_tokens : 0;
  const lspErrors = Object.values(s.lsp).reduce((n, l) => n + l.diagnostics, 0);
  const mcpUp = Object.values(s.mcp).filter((m) => m.state === 'connected').length;
  const link = s.conn === 'live' ? 'Linked' : s.conn === 'reconnecting' ? 'Reconnecting' : s.conn === 'ended' ? 'Ended' : 'Linking';
  return (
    <header class="hud">
      <div class="hud__id">
        <span class="hud-label">{s.project || 'crush'}</span>
        <h1 class="hud__title">{s.session?.title || 'New session'}</h1>
      </div>
      <dl class="hud__readouts">
        <div><dt class="hud-label">Model</dt><dd>{s.model.name || s.model.id || 'Unknown'}</dd></div>
        <div><dt class="hud-label">Tokens</dt><dd>{fmtTokens(tokens)}</dd></div>
        <div><dt class="hud-label">Cost</dt><dd>${(s.session?.cost ?? 0).toFixed(2)}</dd></div>
        <div>
          <dt class="hud-label">Context</dt>
          <dd><meter min={0} max={1} high={0.8} optimum={0} value={fill} aria-label="Context window used" /> {Math.round(fill * 100)}%</dd>
        </div>
        {s.branch && <div><dt class="hud-label">Branch</dt><dd>{s.branch}</dd></div>}
        <div><dt class="hud-label">LSP</dt><dd>{Object.keys(s.lsp).length} · {lspErrors} diag</dd></div>
        <div><dt class="hud-label">MCP</dt><dd>{Object.keys(s.mcp).length} · {mcpUp} up</dd></div>
      </dl>
      <div class="hud__controls">
        <label class="hud-label" for="perm-mode">Permissions</label>
        <select
          id="perm-mode"
          class="hud__select"
          data-mode={s.permissionMode}
          value={s.permissionMode}
          onChange={(e) => void actions.setMode((e.currentTarget as HTMLSelectElement).value as PermissionMode)}
        >
          <option value="normal">Ask</option>
          <option value="yolo">Yolo</option>
          <option value="sysadmin">Sysadmin</option>
        </select>
        <div class="hud__mode" role="group" aria-label="Input mode" data-mode={s.inputMode}>
          <button
            type="button"
            class="hud__mode-btn"
            data-active={s.inputMode === 'plan'}
            aria-pressed={s.inputMode === 'plan'}
            onClick={() => void actions.setInputMode('plan')}
          >Plan</button>
          <button
            type="button"
            class="hud__mode-btn"
            data-active={s.inputMode === 'code'}
            aria-pressed={s.inputMode === 'code'}
            onClick={() => void actions.setInputMode('code')}
          >Code</button>
        </div>
        <MotionMenu />
        <span class="hud__link" data-conn={s.conn} role="status">
          <svg aria-hidden="true" viewBox="0 0 12 12" width="12" height="12"><polygon points="6,1 11,3.5 11,8.5 6,11 1,8.5 1,3.5" /></svg>
          {link}
        </span>
      </div>
    </header>
  );
}
