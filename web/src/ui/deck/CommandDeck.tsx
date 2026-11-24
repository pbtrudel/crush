import { useEffect, useRef, useState } from 'preact/hooks';
import { actions, state } from '../../state/store';
import { api } from '../../api/http';
import { cinematic } from '../../state/cinematic';
import type { Failure } from '../../state/failure';
import { deckMode, openPalette } from '../../state/palette';
import { activeMention, applyMention } from './mention';
import { leaveNoteMode, modeFor, payloadFor } from './modes';

// The gateway reads the whole prompt request through MaxBytesReader with a
// 32 MiB limit (maxPromptBytes, internal/web/api.go), so the multipart
// boundaries, the part headers, the prompt text and the attachments must all
// fit in that one budget.
export const MAX_ATTACHMENT_BYTES = 32 << 20;

// Reserve for the multipart boundaries and part headers; the prompt text and
// the attachments are measured separately.
const MULTIPART_OVERHEAD = 64 << 10;

const ATTACH_TOO_LARGE = 'Attachments must total under 32 MiB.';
const PROMPT_TOO_LARGE = 'The prompt and its attachments must total under 32 MiB.';

export const MENTION_DEBOUNCE_MS = 120;

// attachFiles hands files from another surface (the palette attaches a
// skill as a File) to the mounted deck. It is a no-op when no deck is
// mounted.
let attach: ((files: File[]) => void) | null = null;

export function attachFiles(files: File[]): void {
  attach?.(files);
}

// shellFromPalette starts a shell command from the palette's action: it
// puts a bang in the deck and focuses it. It is a no-op when no deck is
// mounted.
let enterShell: (() => void) | null = null;

export function shellFromPalette(): void {
  enterShell?.();
}

export function CommandDeck() {
  const s = state.value;
  const [text, setText] = useState('');
  const [files, setFiles] = useState<File[]>([]);
  const [failure, setFailure] = useState<Failure | null>(null);
  const [sending, setSending] = useState(false);
  // The caret drives the suggestion list: a cursor that leaves the mention
  // closes it, and the effect below cannot read it from the element because
  // the text state and the DOM selection move on separate renders.
  const [caret, setCaret] = useState(0);
  const [suggest, setSuggest] = useState<{ start: number; items: string[]; index: number } | null>(null);
  const root = useRef<HTMLFormElement>(null);
  const area = useRef<HTMLTextAreaElement>(null);
  const picker = useRef<HTMLInputElement>(null);
  // Completion requests can resolve out of order; only the newest one may
  // touch the suggestion list.
  const request = useRef(0);
  // The sending state lags a render, so a ref blocks a second send while the
  // first request is in flight.
  const inFlight = useRef(false);
  const ended = s.conn === 'ended';
  const mode = modeFor(text, deckMode.value);

  // signin.css places the AWS SSO card above the deck from --deck-h, so the
  // deck publishes its own height instead of a hard-coded offset.
  useEffect(() => {
    const el = root.current;
    if (!el) return;
    const doc = document.documentElement;
    const publish = () => {
      const height = Math.round(el.getBoundingClientRect().height);
      if (height > 0) doc.style.setProperty('--deck-h', `${height}px`);
    };
    publish();
    const observer = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(publish);
    observer?.observe(el);
    return () => {
      observer?.disconnect();
      doc.style.removeProperty('--deck-h');
    };
  }, []);

  // The palette attaches through this module function; re-register when the
  // deck's file state moves so it reads the current list and budget.
  useEffect(() => {
    attach = addFiles;
    return () => {
      if (attach === addFiles) attach = null;
    };
  }, [files, ended]);

  // The palette's shell action goes through this module function, like the
  // file attachment above, so it reads the deck's current text.
  useEffect(() => {
    const enter = () => {
      if (ended) return;
      const next = text.startsWith('!') ? text : `!${text}`;
      // The action leaves note mode: the bang is what makes the mode here.
      leaveNoteMode();
      setText(next);
      setCaret(next.length);
      requestAnimationFrame(() => {
        const el = area.current;
        if (!el) return;
        el.focus();
        el.setSelectionRange(next.length, next.length);
      });
    };
    enterShell = enter;
    return () => {
      if (enterShell === enter) enterShell = null;
    };
  }, [text, ended]);

  useEffect(() => {
    const m = activeMention(text, caret);
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
    return () => {
      // Invalidate a response already in flight so it cannot set state after
      // the text changed or the deck unmounted.
      request.current++;
      window.clearTimeout(t);
    };
  }, [text, caret]);

  const addFiles = (list: FileList | File[] | null) => {
    if (!list || ended) return;
    const added = Array.from(list);
    const total = files.concat(added).reduce((n, f) => n + f.size, 0);
    if (total >= MAX_ATTACHMENT_BYTES) {
      setFailure({ tone: 'error', text: ATTACH_TOO_LARGE });
      return;
    }
    setFailure(null);
    setFiles((cur) => [...cur, ...added]);
  };

  const pick = (path: string) => {
    const el = area.current!;
    const next = applyMention(text, suggest!.start, el.selectionStart, path);
    setText(next.text);
    setCaret(next.caret);
    setSuggest(null);
    requestAnimationFrame(() => { el.focus(); el.setSelectionRange(next.caret, next.caret); });
  };

  const submit = async () => {
    if (inFlight.current) return;
    inFlight.current = true;
    try {
      // The mode is read here, not from the render: a palette action and
      // the submit can land in the same tick.
      const current = modeFor(text, deckMode.value);
      const payload = payloadFor(current, text);
      if (!payload.trim()) {
        setFailure({
          tone: 'error',
          text: current === 'shell'
            ? 'Type a command to run.'
            : current === 'note' ? 'Type a note to keep.' : 'Type a task before transmitting.',
        });
        return;
      }
      if (current === 'prompt') {
        const attachmentBytes = files.reduce((n, f) => n + f.size, 0);
        const promptBytes = new TextEncoder().encode(text).length;
        if (attachmentBytes + promptBytes + MULTIPART_OVERHEAD >= MAX_ATTACHMENT_BYTES) {
          setFailure({ tone: 'error', text: PROMPT_TOO_LARGE });
          return;
        }
      }
      setFailure(null);
      setSending(true);
      if (current === 'shell') {
        const failed = await actions.runShell(payload);
        if (failed) { setFailure(failed); return; }
        setText('');
        return;
      }
      if (current === 'note') {
        const failed = await actions.addNote(payload);
        if (failed) { setFailure(failed); return; }
        setText('');
        return;
      }
      const failure = await actions.sendPrompt(text, files);
      if (failure) { setFailure(failure); return; }
      setText('');
      setFiles([]);
    } finally {
      inFlight.current = false;
      setSending(false);
    }
  };

  const onKeyDown = (e: KeyboardEvent) => {
    // Safari reports the Enter that ends a composition after compositionend
    // with keyCode 229, so isComposing alone is not enough.
    const composing = e.isComposing || e.keyCode === 229;
    // A slash on an empty deck opens the palette, the terminal's slash
    // command entry; the character itself never lands in the prompt. A note
    // is plain text, so the slash is just a slash there.
    if (!composing && e.key === '/' && text === '' && deckMode.value !== 'note'
      && !e.ctrlKey && !e.metaKey && !e.altKey) {
      e.preventDefault();
      openPalette('all');
      return;
    }
    if (suggest) {
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault();
        const d = e.key === 'ArrowDown' ? 1 : -1;
        setSuggest({ ...suggest, index: (suggest.index + d + suggest.items.length) % suggest.items.length });
        return;
      }
      if (!composing && (e.key === 'Enter' || e.key === 'Tab')) {
        e.preventDefault();
        pick(suggest.items[suggest.index]);
        return;
      }
      if (e.key === 'Escape') { e.preventDefault(); setSuggest(null); return; }
    }
    // Escape leaves note mode and keeps the draft; the same press does
    // nothing else, so it never reaches the run cancel below or the App's
    // inspector and cinematic handlers.
    if (e.key === 'Escape' && deckMode.value === 'note') {
      e.preventDefault();
      leaveNoteMode();
      return;
    }
    if (!composing && e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); void submit(); }
    if (e.key === 'Escape' && s.busy) { e.preventDefault(); void actions.cancel(); }
  };

  return (
    <form
      ref={root}
      class={`deck glass${cinematic.value ? ' deck--slim' : ''}`}
      aria-label="Command deck"
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
      {(s.inputMode === 'plan' || mode !== 'prompt') && (
        <div class="deck__badges">
          {s.inputMode === 'plan' && <p class="deck__plan-badge">Plan mode</p>}
          {mode !== 'prompt' && (
            <p class="deck__mode-badge" data-mode={mode}>{mode === 'shell' ? 'Shell' : 'Note'}</p>
          )}
        </div>
      )}
      <label class="hud-label" for="deck-input">Transmit to crush</label>
      {!s.agentReady && <p class="deck__starting" role="status">Crush is starting the agent</p>}
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
          onInput={(e) => {
            const el = e.currentTarget as HTMLTextAreaElement;
            setText(el.value);
            setCaret(el.selectionStart);
          }}
          onKeyDown={onKeyDown}
          onKeyUp={(e) => setCaret((e.currentTarget as HTMLTextAreaElement).selectionStart)}
          onClick={(e) => setCaret((e.currentTarget as HTMLTextAreaElement).selectionStart)}
          onSelect={(e) => setCaret((e.currentTarget as HTMLTextAreaElement).selectionStart)}
          onPaste={(e) => {
            const imgs = Array.from(e.clipboardData?.files ?? []).filter((f) => f.type.startsWith('image/'));
            if (imgs.length) addFiles(imgs);
          }}
        />
        <div class="deck__actions">
          <input ref={picker} type="file" multiple hidden onChange={(e) => addFiles((e.currentTarget as HTMLInputElement).files)} />
          <button type="button" class="btn btn--ghost" disabled={ended} onClick={() => picker.current?.click()}>Attach</button>
          {s.busy && <button type="button" class="btn btn--danger" onClick={() => void actions.cancel()}>Cancel run</button>}
          <button type="submit" class="btn btn--primary" disabled={ended || sending} aria-busy={sending}>
            {mode === 'shell'
              ? (sending ? 'Running' : 'Run')
              : mode === 'note' ? (sending ? 'Keeping' : 'Keep note') : (sending ? 'Transmitting' : s.busy ? 'Queue task' : 'Transmit')}
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
        {mode === 'shell'
          ? 'Run a shell command in the workspace'
          : mode === 'note' ? 'Kept for the next prompt; starts no run'
            : ended
              ? 'This crush process has exited. Start crush with --web to link a new tab.'
              : 'Enter to send, Shift+Enter for a new line, Esc to cancel a run. Drop or paste files to attach.'}
      </p>
      <p
        id="deck-error"
        class="deck__error"
        data-tone={failure?.tone ?? 'error'}
        role={failure?.tone === 'starting' ? 'status' : 'alert'}
      >
        {failure?.text ?? ''}
        {failure?.tone === 'starting' && (
          <button type="button" class="link-button deck__retry" onClick={() => void submit()}>Retry</button>
        )}
      </p>
    </form>
  );
}
