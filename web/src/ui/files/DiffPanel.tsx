import { useEffect, useRef, useState } from 'preact/hooks';
import { ApiError, api } from '../../api/http';
import type { FileDiff, Message } from '../../api/types';
import { findAgent, type Agent } from '../../state/agents';
import { diffMode, setDiffMode, type DiffMode } from '../../state/drawer';
import { focusToolCall } from '../../state/focusTool';
import { actions, state } from '../../state/store';
import { relativePath } from './fileGroups';

// toolCallName finds the name of a call the loaded messages hold, so the
// drawer can say which tool wrote a version.
function toolCallName(messages: Message[], id: string): string | null {
  for (const m of messages) {
    for (const p of m.parts) {
      if (p.type === 'tool_call' && p.data.id === id) return p.data.name;
    }
  }
  return null;
}

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

const LAYOUTS: Array<[DiffMode, string]> = [['split', 'Split'], ['unified', 'Unified']];

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
      .then((d) => {
        if (!live) return;
        // A newer history version refetches the same range; keeping the old
        // object keeps the mounted editor and its scroll position.
        setDiff((prev) => (prev && prev.from === d.from && prev.to === d.to && prev.unified === d.unified ? prev : d));
      })
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
      .catch(() => {
        if (!live) return;
        // The host is already in the DOM: drop the box mountDiff attached
        // before it failed, so no empty frame sits beside the error.
        el.shadowRoot?.replaceChildren();
        setError('The diff view could not load');
      });
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
  // The versions the range shows, newest side first, each with the tool call
  // that recorded it when one was tagged.
  const made = diff
    ? (diff.from === diff.to ? [diff.to] : [diff.to, diff.from]).map((version) => {
      const index = versions.indexOf(version);
      const tool = index < 0 ? undefined : file?.version_tools[index];
      return { version, sessionId: tool?.session_id, callId: tool?.tool_call_id };
    })
    : [];

  const versionOptions = versions.map((v) => <option key={v} value={String(v)}>Version {v}</option>);

  return (
    <section class="diffpanel" aria-labelledby="diff-title">
      <header class="diffpanel__head">
        <h3 id="diff-title" class="diffpanel__title" title={rel}>{rel}</h3>
        {editors.length > 0 && (
          <p class="diffpanel__editors hud-label">Edited by{' '}
            {editors.map((a) => (
              <button key={a.sessionId} type="button" class="chip" aria-label={`Inspect ${a.name}`} onClick={() => void actions.inspect(a.sessionId)}>{a.name}</button>
            ))}
          </p>
        )}
        <div class="diffpanel__controls">
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
        </div>
      </header>
      {made.length > 0 && (
        <p class="diffpanel__made hud-label">
          {made.map(({ version, sessionId, callId }) => (
            <span key={version} class="diffpanel__made-line">
              {callId && sessionId
                ? (
                  <button type="button" class="link-button" onClick={() => focusToolCall(sessionId, callId)}>
                    Made by {toolCallName(s.messages, callId) ?? 'a tool'} in {findAgent(s, sessionId)?.name ?? 'another agent'}
                  </button>
                )
                : 'Recorded before the change'}
            </span>
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
        ? (diff.unified
            ? (
              <>
                <p class="hud-label">This diff is over 1 MB, so only the unified text is shown.</p>
                <UnifiedText text={diff.unified} />
              </>
            )
            : <p class="hud-label">These files are too large to diff.</p>)
        : <div class="diffpanel__view" ref={host} />}
    </section>
  );
}
