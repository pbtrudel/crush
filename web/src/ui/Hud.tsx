import { useEffect, useRef, useState } from 'preact/hooks';
import { state, actions } from '../state/store';
import { contextFill } from '../state/reducer';
import type { PermissionMode } from '../api/types';
import { telemetry } from '../state/telemetry';
import { cinematic, cinematicAllowed, setCinematic } from '../state/cinematic';
import { openModels, openSessions } from '../state/palette';
import { openDisplay } from '../state/display';
import { ActivityChart } from './hud/ActivityChart';
import { PinToggle } from './hud/PinToggle';
import { ContextGauge } from './hud/ContextGauge';
import { Numeral } from './hud/Numeral';
import { ServerHexes } from './hud/ServerHexes';
import { Sparkline } from './hud/Sparkline';
import { usePublishedHeight } from './usePublishedHeight';

const fmtTokens = (n: number) => (n >= 1000 ? `${(n / 1000).toFixed(1)}k` : String(n));

const TICK_MS = 15_000;

export function Hud() {
  const s = state.value;
  const { tools, usage } = telemetry.value;
  const fill = contextFill(s);
  const tokens = s.session ? s.session.prompt_tokens + s.session.completion_tokens : 0;
  const [now, setNow] = useState(() => Date.now());
  const canFly = cinematicAllowed();
  const root = useRef<HTMLElement>(null);
  const link = s.conn === 'live' ? 'Linked' : s.conn === 'reconnecting' ? 'Reconnecting' : s.conn === 'ended' ? 'Ended' : 'Linking';
  // The short word is all the strip has room for; a lost or ended link
  // carries a sentence the live region reads instead.
  const linkDetail = s.conn === 'reconnecting'
    ? 'Signal lost. Reconnecting to crush.'
    : s.conn === 'ended'
      ? s.endedReason === 'detached'
        ? 'Web mode was stopped. Run /web in the terminal for a new link.'
        : 'Session ended. The log stays readable. Start crush with --web or run /web to link a new tab.'
      : '';

  // shell.css places the toasts from --hud-h, so the strip publishes its own
  // height instead of a hard-coded offset.
  usePublishedHeight(root, '--hud-h');

  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), TICK_MS);
    return () => clearInterval(timer);
  }, []);

  // A new tool call moves the current minute, so read the clock with it.
  useEffect(() => {
    setNow(Date.now());
  }, [tools]);

  return (
    <header
      class="hud holo scanlines flicker-once"
      ref={root}
      data-browsing={s.browsing !== null ? 'true' : undefined}
    >
      <div class={`hud__id${cinematic.value ? ' visually-hidden' : ''}`}>
        <span class="hud-label">{s.project || 'crush'}</span>
        <h1 class="hud__title">
          {/* The identity block is off screen in cinematic mode, so an
              invisible control must not take a tab stop. */}
          <button
            type="button"
            class="hud__title-btn"
            tabIndex={cinematic.value ? -1 : 0}
            aria-label={`Session: ${s.session?.title || 'New session'}. Switch session`}
            onClick={() => openSessions()}
          >{s.session?.title || 'New session'}</button>
        </h1>
        <p class="hud__meta">
          {/* The model name opens the selector; cinematic mode hides this
              block, so the control leaves the tab order with it. */}
          <button
            type="button"
            class="hud__model"
            tabIndex={cinematic.value ? -1 : 0}
            aria-label={`Model: ${s.model.name || s.model.id || 'Unknown'}. Switch model`}
            title={s.model.name || s.model.id || 'Unknown'}
            onClick={() => openModels()}
          >
            <svg class="hud__provider" aria-hidden="true" viewBox="0 0 12 12" width="10" height="10"><polygon points="6,1 11,6 6,11 1,6" /></svg>
            <span class="hud__model-name">{s.model.name || s.model.id || 'Unknown'}</span>
          </button>
          {s.branch && <span class="hud__branch" title={s.branch}>{s.branch}</span>}
        </p>
      </div>
      <ContextGauge fill={fill} />
      <div class="hud__metric">
        <span class="hud-label" aria-hidden="true">Tokens</span>
        <Numeral label="Tokens" text={fmtTokens(tokens)} />
        <Sparkline values={usage.map((u) => u.tokens)} />
      </div>
      <div class="hud__metric">
        <span class="hud-label" aria-hidden="true">Cost</span>
        <Numeral label="Cost" text={`$${(s.session?.cost ?? 0).toFixed(2)}`} />
        <Sparkline values={usage.map((u) => u.cost)} />
      </div>
      <div class="hud__chart">
        <span class="hud-label" aria-hidden="true">Tools 15m</span>
        <ActivityChart events={tools} now={now} />
      </div>
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
        <PinToggle />
        <button type="button" class="hud-label hud__display" onClick={() => openDisplay()}>Display</button>
        {canFly && (
          <button
            type="button"
            class="hud__mode-btn hud__cinematic"
            aria-pressed={cinematic.value}
            title="Cinematic mode (Alt+C)"
            onClick={() => setCinematic(!cinematic.value)}
          >Cinematic</button>
        )}
        {/* The one polite live region for the connection: the banner below
            repeats it for sighted users and stays out of the a11y tree. */}
        <span class="hud__link" data-conn={s.conn} role="status">
          <svg aria-hidden="true" viewBox="0 0 12 12" width="12" height="12"><polygon points="6,1 11,3.5 11,8.5 6,11 1,8.5 1,3.5" /></svg>
          <span aria-hidden="true">{link}</span>
          <span class="visually-hidden">{linkDetail || link}</span>
        </span>
      </div>
    </header>
  );
}
