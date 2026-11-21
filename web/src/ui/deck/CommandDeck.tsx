import { useEffect, useRef, useState } from 'preact/hooks';
import { actions, NO_SESSION_HINT, state } from '../../state/store';
import { api } from '../../api/http';
import { activeMention, applyMention } from './mention';

// The gateway caps the whole prompt body at 32 MiB (multipart overhead and
// the prompt included), so the deck rejects attachments that leave no room.
export const MAX_ATTACHMENT_BYTES = 32 << 20;

export const MENTION_DEBOUNCE_MS = 120;

export function CommandDeck() {
  const s = state.value;
  const [text, setText] = useState('');
  const [files, setFiles] = useState<File[]>([]);
  const [error, setError] = useState('');
  const [sending, setSending] = useState(false);
  const [suggest, setSuggest] = useState<{ start: number; items: string[]; index: number } | null>(null);
  const area = useRef<HTMLTextAreaElement>(null);
  const picker = useRef<HTMLInputElement>(null);
  // Completion requests can resolve out of order; only the newest one may
  // touch the suggestion list.
  const request = useRef(0);
  const ended = s.conn === 'ended';
  // M1 never starts a session from the browser: sending waits for the
  // terminal to have one (starting a session here lands with M3).
  const noSession = s.sessionId === '';

  useEffect(() => {
    const el = area.current;
    if (!el) return;
    const m = activeMention(text, el.selectionStart);
    const id = ++request.current;
    if (!m) { setSuggest(null); return; }
    const t = window.setTimeout(async () => {
      try {
        const { files: items } = await api.completeFiles(m.query);
        if (id !== request.current) return;
        setSuggest(items.length ? { start: m.start, items, index: 0 } : null);
      } catch {
        if (id !== request.current) return;
        setSuggest(null);
      }
    }, MENTION_DEBOUNCE_MS);
    return () => window.clearTimeout(t);
  }, [text]);

  const addFiles = (list: FileList | File[] | null) => {
    if (!list) return;
    const added = Array.from(list);
    const total = files.concat(added).reduce((n, f) => n + f.size, 0);
    if (total >= MAX_ATTACHMENT_BYTES) {
      setError('Attachments must total under 32 MiB.');
      return;
    }
    setError('');
    setFiles((cur) => [...cur, ...added]);
  };

  const pick = (path: string) => {
    const el = area.current!;
    const next = applyMention(text, suggest!.start, el.selectionStart, path);
    setText(next.text);
    setSuggest(null);
    requestAnimationFrame(() => { el.focus(); el.setSelectionRange(next.caret, next.caret); });
  };

  const submit = async () => {
    if (!text.trim()) { setError('Type a task before transmitting.'); return; }
    if (!s.sessionId) { setError(NO_SESSION_HINT); return; }
    setError('');
    setSending(true);
    const err = await actions.sendPrompt(text, files);
    setSending(false);
    if (err) { setError(err); return; }
    setText('');
    setFiles([]);
  };

  const onKeyDown = (e: KeyboardEvent) => {
    if (suggest) {
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault();
        const d = e.key === 'ArrowDown' ? 1 : -1;
        setSuggest({ ...suggest, index: (suggest.index + d + suggest.items.length) % suggest.items.length });
        return;
      }
      if (e.key === 'Enter' || e.key === 'Tab') { e.preventDefault(); pick(suggest.items[suggest.index]); return; }
      if (e.key === 'Escape') { e.preventDefault(); setSuggest(null); return; }
    }
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) { e.preventDefault(); void submit(); }
    if (e.key === 'Escape' && s.busy) { e.preventDefault(); void actions.cancel(); }
  };

  return (
    <form
      class="deck"
      data-busy={s.busy}
      onSubmit={(e) => { e.preventDefault(); void submit(); }}
      onDragOver={(e) => e.preventDefault()}
      onDrop={(e) => { e.preventDefault(); addFiles(e.dataTransfer?.files ?? null); }}
    >
      {s.queued.length > 0 && (
        <div class="deck__queue">
          <span class="hud-label">Queued</span>
          {s.queued.map((q, i) => <span key={i} class="chip">{q.length > 48 ? `${q.slice(0, 48)}…` : q}</span>)}
          <button type="button" class="link-button" onClick={() => void actions.clearQueue()}>Clear queue</button>
        </div>
      )}
      {files.length > 0 && (
        <ul class="deck__files" aria-label="Attachments">
          {files.map((f, i) => (
            <li key={i} class="chip">
              {f.name}
              <button type="button" aria-label={`Remove ${f.name}`} onClick={() => setFiles(files.filter((_, j) => j !== i))}>×</button>
            </li>
          ))}
        </ul>
      )}
      <label class="hud-label" for="deck-input">Transmit to crush</label>
      <div class="deck__row">
        <textarea
          id="deck-input"
          ref={area}
          rows={2}
          value={text}
          disabled={ended}
          placeholder={ended ? 'Session ended' : 'Describe a task. Use @ to reference a file.'}
          aria-describedby="deck-help deck-error"
          aria-autocomplete="list"
          aria-controls={suggest ? 'deck-suggest' : undefined}
          aria-activedescendant={suggest ? `deck-suggest-${suggest.index}` : undefined}
          onInput={(e) => setText((e.currentTarget as HTMLTextAreaElement).value)}
          onKeyDown={onKeyDown}
          onPaste={(e) => {
            const imgs = Array.from(e.clipboardData?.files ?? []).filter((f) => f.type.startsWith('image/'));
            if (imgs.length) addFiles(imgs);
          }}
        />
        <div class="deck__actions">
          <input ref={picker} type="file" multiple hidden onChange={(e) => addFiles((e.currentTarget as HTMLInputElement).files)} />
          <button type="button" class="btn btn--ghost" disabled={ended} onClick={() => picker.current?.click()}>Attach</button>
          {s.busy && <button type="button" class="btn btn--danger" onClick={() => void actions.cancel()}>Cancel run</button>}
          <button type="submit" class="btn btn--primary" disabled={ended || noSession} aria-busy={sending}>
            {sending ? 'Transmitting' : s.busy ? 'Queue task' : 'Transmit'}
          </button>
        </div>
      </div>
      {suggest && (
        <ul id="deck-suggest" class="deck__suggest" role="listbox" aria-label="Files">
          {suggest.items.map((p, i) => (
            <li
              id={`deck-suggest-${i}`}
              key={p}
              role="option"
              aria-selected={i === suggest.index}
              onMouseDown={(e) => { e.preventDefault(); pick(p); }}
            >{p}</li>
          ))}
        </ul>
      )}
      <p id="deck-help" class="deck__help">
        {ended
          ? 'This crush process has exited. Start crush with --web to link a new tab.'
          : noSession
            ? `${NO_SESSION_HINT}.`
            : 'Enter to send, Shift+Enter for a new line, Esc to cancel a run. Drop or paste files to attach.'}
      </p>
      <p id="deck-error" class="deck__error" role="alert">{error}</p>
    </form>
  );
}
