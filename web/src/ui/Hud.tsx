import { useEffect, useRef, useState } from 'preact/hooks';
import { state, actions } from '../state/store';
import { contextFill } from '../state/reducer';
import type { PermissionMode } from '../api/types';
import { telemetry } from '../state/telemetry';
import { MotionMenu } from './MotionMenu';
import { ActivityChart } from './hud/ActivityChart';
import { ContextGauge } from './hud/ContextGauge';
import { Numeral } from './hud/Numeral';
import { ServerHexes } from './hud/ServerHexes';
import { Sparkline } from './hud/Sparkline';

const fmtTokens = (n: number) => (n >= 1000 ? `${(n / 1000).toFixed(1)}k` : String(n));

const TICK_MS = 15_000;

export function Hud() {
  const s = state.value;
  const { tools, usage } = telemetry.value;
  const fill = contextFill(s);
  const tokens = s.session ? s.session.prompt_tokens + s.session.completion_tokens : 0;
  const [now, setNow] = useState(() => Date.now());
  const root = useRef<HTMLElement>(null);
  const link = s.conn === 'live' ? 'Linked' : s.conn === 'reconnecting' ? 'Reconnecting' : s.conn === 'ended' ? 'Ended' : 'Linking';

  // shell.css places the toasts from --hud-h, so the strip publishes its own
  // height instead of a hard-coded offset.
  useEffect(() => {
    const el = root.current;
    if (!el) return;
    const doc = document.documentElement;
    const publish = () => {
      const height = Math.round(el.getBoundingClientRect().height);
      if (height > 0) doc.style.setProperty('--hud-h', `${height}px`);
    };
    publish();
    const observer = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(publish);
    observer?.observe(el);
    return () => {
      observer?.disconnect();
      doc.style.removeProperty('--hud-h');
    };
  }, []);

  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), TICK_MS);
    return () => clearInterval(timer);
  }, []);

  // A new tool call moves the current minute, so read the clock with it.
  useEffect(() => {
    setNow(Date.now());
  }, [tools]);

  return (
    <header class="hud holo scanlines flicker-once" ref={root}>
      <div class="hud__id">
        <span class="hud-label">{s.project || 'crush'}</span>
        <h1 class="hud__title">{s.session?.title || 'New session'}</h1>
        <p class="hud__meta">
          <span class="hud__model">
            <svg class="hud__provider" aria-hidden="true" viewBox="0 0 12 12" width="10" height="10"><polygon points="6,1 11,6 6,11 1,6" /></svg>
            {s.model.name || s.model.id || 'Unknown'}
          </span>
          {s.branch && <span class="hud__branch">{s.branch}</span>}
        </p>
      </div>
      <ContextGauge fill={fill} />
      <div class="hud__metric">
        <Numeral label="Tokens" text={fmtTokens(tokens)} />
        <Sparkline values={usage.map((u) => u.tokens)} label="Token trend" />
      </div>
      <div class="hud__metric">
        <Numeral label="Cost" text={`$${(s.session?.cost ?? 0).toFixed(2)}`} />
        <Sparkline values={usage.map((u) => u.cost)} label="Cost trend" />
      </div>
      <ActivityChart events={tools} now={now} />
      <div class="hud__servers">
        <span class="hud-label">LSP</span>
        <ServerHexes kind="lsp" servers={s.lsp} />
        <span class="hud-label">MCP</span>
        <ServerHexes kind="mcp" servers={s.mcp} />
      </div>
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
