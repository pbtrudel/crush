import { useEffect, useRef, useState } from 'preact/hooks';
import type { GrantAction } from '../../api/http';
import type { PermissionRequest } from '../../api/types';
import { actions } from '../../state/store';
import { useDialogFocus } from './focus';

const UNREACHABLE = 'The request did not reach crush';

function params(p: unknown): string {
  if (p == null) return '';
  return typeof p === 'string' ? p : JSON.stringify(p, null, 2);
}

export function PermissionDialog({ req, more, disabled = false }: { req: PermissionRequest; more: number; disabled?: boolean }) {
  const root = useRef<HTMLDivElement>(null);
  // The sending state lags a render, so a ref blocks a second decision while
  // the first grant is in flight.
  const busy = useRef(false);
  const [sending, setSending] = useState(false);
  // A decision that reached the gateway must not be repeated while the
  // permission request is still in state; only a failure re-arms the dialog.
  const [settled, setSettled] = useState(false);
  const [error, setError] = useState('');
  const lastId = useRef(req.id);
  useDialogFocus(root, req.id);

  useEffect(() => {
    if (lastId.current === req.id) return;
    lastId.current = req.id;
    busy.current = false;
    setSending(false);
    setSettled(false);
    setError('');
  }, [req.id]);

  // decide sends one answer. After the first, further keys and clicks are
  // ignored until the dialog goes away; only a failure re-arms them.
  const decide = async (action: GrantAction) => {
    if (busy.current || settled || disabled) return;
    busy.current = true;
    setSending(true);
    setError('');
    try {
      const err = await actions.grant(req.id, action);
      if (err) setError(err);
      else setSettled(true);
    } catch {
      setError(UNREACHABLE);
    } finally {
      busy.current = false;
      setSending(false);
    }
  };

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      // A held key repeats; the second grant would report a false
      // "Already answered in the terminal". IME keys are not decisions and
      // modified keys belong to the browser or the OS.
      if (e.repeat || e.isComposing || e.keyCode === 229) return;
      if (e.ctrlKey || e.metaKey || e.altKey) return;
      const k = e.key.toLowerCase();
      if (k !== 'a' && k !== 's' && k !== 'd') return;
      e.preventDefault();
      if (k === 'a') void decide('allow');
      if (k === 's') void decide('allow_session');
      if (k === 'd') void decide('deny');
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [req.id, disabled, settled]);

  const locked = disabled || sending || settled;
  return (
    <div
      ref={root}
      class="interrupt"
      role="alertdialog"
      aria-modal="true"
      aria-labelledby="perm-title"
      aria-describedby="perm-desc"
      data-danger={req.danger ? 'high' : 'normal'}
      tabIndex={-1}
    >
      <header>
        <p class="hud-label">Permission required{more > 0 ? ` · ${more} more waiting` : ''}</p>
        <h2 id="perm-title" class="interrupt__title">{req.tool_name}</h2>
      </header>
      <p id="perm-desc">{req.description || req.action}</p>
      {req.danger && <p class="interrupt__danger">{req.danger}</p>}
      {req.path && <p class="interrupt__path"><span class="hud-label">Path</span> <code>{req.path}</code></p>}
      {params(req.params) && <pre class="interrupt__params">{params(req.params)}</pre>}
      {error && <p class="interrupt__error" role="alert">{error}</p>}
      <div class="interrupt__actions">
        <button type="button" class="btn btn--primary" disabled={locked} aria-busy={sending} onClick={() => void decide('allow')}><kbd>A</kbd> Allow once</button>
        <button type="button" class="btn" disabled={locked} aria-busy={sending} onClick={() => void decide('allow_session')}><kbd>S</kbd> Allow for session</button>
        <button type="button" class="btn btn--danger" disabled={locked} aria-busy={sending} onClick={() => void decide('deny')}><kbd>D</kbd> Deny</button>
      </div>
    </div>
  );
}
