import { useEffect, useRef, useState } from 'preact/hooks';
import { ApiError, api } from '../../api/http';
import type { FileDiff } from '../../api/types';
import { findAgent, type Agent } from '../../state/agents';
import { diffMode, setDiffMode, type DiffMode } from '../../state/drawer';
import { actions, state } from '../../state/store';
import { relativePath } from './fileGroups';

function lineKind(line: string): 'add' | 'rem' | 'hunk' | 'ctx' {
  if (line.startsWith('@@')) return 'hunk';
  if (line.startsWith('+') && !line.startsWith('+++')) return 'add';
  if (line.startsWith('-') && !line.startsWith('---')) return 'rem';
  return 'ctx';
}

function UnifiedText({ text }: { text: string }) {
  // tabIndex lets a keyboard user scroll the long fallback.
  return (
    <pre class="diff-unified" tabIndex={0}>
      {text.split('\n').map((line, i) => <span key={i} data-kind={lineKind(line)}>{line}{'\n'}</span>)}
    </pre>
  );
}

const LAYOUTS: Array<[DiffMode, string]> = [['split', 'Side by side'], ['unified', 'Unified']];

export function DiffPanel({ path }: { path: string }) {
  const s = state.value;
  const file = s.files.find((f) => f.path === path);
  const versions = file?.version_ids ?? [];
  const latest = versions.at(-1);
  const [range, setRange] = useState<{ from?: number; to?: number }>({});
  const [diff, setDiff] = useState<FileDiff | null>(null);
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);
  const host = useRef<HTMLDivElement>(null);
  const mode = diffMode.value;
  const rel = relativePath(path, s.workingDir);
  const sid = s.sessionId;

  useEffect(() => {
    setRange({});
    setDiff(null);
  }, [path]);

  useEffect(() => {
    if (!sid) return;
    let live = true;
    setError('');
    api.diff(sid, path, range.from, range.to)
      .then((d) => { if (live) setDiff(d); })
      .catch((err) => { if (live) setError(err instanceof ApiError ? err.message : 'The diff did not load'); });
    return () => { live = false; };
  }, [sid, path, range.from, range.to, latest, attempt]);

  useEffect(() => {
    const el = host.current;
    if (!el || !diff || diff.truncated) return;
    let live = true;
    let destroy: (() => void) | null = null;
    import('./diff/codemirror')
      .then(({ mountDiff }) => mountDiff(el, diff, mode, rel))
      .then((stop) => { if (live) destroy = stop; else stop(); })
      .catch(() => { if (live) setError('The diff view could not load'); });
    return () => {
      live = false;
      destroy?.();
    };
  }, [diff, mode]);

  const pick = (key: 'from' | 'to') => (e: Event) => {
    const v = Number((e.currentTarget as HTMLSelectElement).value);
    setRange({ from: diff?.from, to: diff?.to, [key]: v });
  };
  const editors = (file?.session_ids ?? []).map((id) => findAgent(s, id)).filter((a): a is Agent => a !== null);

  const versionOptions = versions.map((v) => <option key={v} value={String(v)}>Version {v}</option>);

  return (
    <section class="diffpanel" aria-labelledby="diff-title">
      <header class="diffpanel__head">
        <h3 id="diff-title" class="diffpanel__title">{rel}</h3>
        <label class="hud-label">From{' '}
          <select class="hud__select" name="from" value={String(diff?.from ?? '')} disabled={!diff} onChange={pick('from')}>
            {versionOptions}
          </select>
        </label>
        <label class="hud-label">To{' '}
          <select class="hud__select" name="to" value={String(diff?.to ?? '')} disabled={!diff} onChange={pick('to')}>
            {versionOptions}
          </select>
        </label>
        <div class="hud__mode" role="group" aria-label="Diff layout">
          {LAYOUTS.map(([m, label]) => (
            <button key={m} type="button" class="hud__mode-btn" data-mode={m} data-active={mode === m} aria-pressed={mode === m} onClick={() => setDiffMode(m)}>{label}</button>
          ))}
        </div>
      </header>
      {editors.length > 0 && (
        <p class="diffpanel__editors hud-label">Edited by{' '}
          {editors.map((a) => (
            <button key={a.sessionId} type="button" class="chip" aria-label={`Inspect ${a.name}`} onClick={() => void actions.inspect(a.sessionId)}>{a.name}</button>
          ))}
        </p>
      )}
      {error && (
        <p class="diffpanel__error" role="alert">{error}{' '}
          <button type="button" class="link-button" onClick={() => setAttempt((n) => n + 1)}>Retry</button>
        </p>
      )}
      {!diff && !error && <p class="hud-label">Loading the diff</p>}
      {diff?.truncated
        ? (
          <>
            <p class="hud-label">This diff is over 1 MB, so only the unified text is shown.</p>
            {diff.unified
              ? <UnifiedText text={diff.unified} />
              : <p class="hud-label">Diff text not available.</p>}
          </>
        )
        : <div class="diffpanel__view" ref={host} />}
    </section>
  );
}
