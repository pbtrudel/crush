import { useEffect, useLayoutEffect, useRef, useState } from 'preact/hooks';
import type { SecureKind, SecurePrompt } from '../../api/types';
import { describeFailure, type Failure } from '../../state/failure';
import { actions } from '../../state/store';
import { useDialogFocus } from './focus';

// SECRET_LABELS names the field for each prompt that takes typed text. A
// confirm prompt takes none and a touch prompt is answered by the key
// itself.
const SECRET_LABELS: Partial<Record<SecureKind, string>> = {
  passphrase: 'Passphrase',
  pin: 'PIN',
  password: 'Password',
};

// SecurePromptDialog answers a GPG pinentry or SSH askpass prompt. It is the
// first thing Interrupt shows: the terminal is blocked on this answer.
export function SecurePromptDialog({ prompt, disabled }: { prompt: SecurePrompt; disabled: boolean }) {
  const root = useRef<HTMLDivElement>(null);
  const input = useRef<HTMLInputElement>(null);
  const primary = useRef<HTMLButtonElement>(null);
  // The sending state lags a render, so a ref blocks a second answer while
  // the first one is in flight.
  const busy = useRef(false);
  // A result that arrives after this dialog is gone must not touch it.
  const alive = useRef(true);
  // generation names the prompt the dialog is on. A prompt replaced while a
  // request is in flight must not take the old result, so a continuation
  // compares both the generation and the id it started with.
  const generation = useRef(0);
  const currentId = useRef(prompt.id);
  currentId.current = prompt.id;
  const lastId = useRef(prompt.id);
  const [sending, setSending] = useState(false);
  // An answer that reached the gateway must not repeat while the frame
  // removing the prompt is on the way; only a failure re-arms the dialog.
  const [settled, setSettled] = useState(false);
  const [failure, setFailure] = useState<Failure | null>(null);
  // Changing the focus key pulls focus back into the dialog after a
  // reconnect.
  const [focusTick, setFocusTick] = useState(0);
  const wasDisabled = useRef(disabled);
  useDialogFocus(root, `${prompt.id}:${focusTick}`);

  const touch = prompt.kind === 'touch';
  const secretLabel = SECRET_LABELS[prompt.kind];
  const locked = disabled || sending || settled;
  const alertText = failure?.text ?? prompt.retry;

  useEffect(() => () => {
    alive.current = false;
  }, []);

  useEffect(() => {
    if (lastId.current === prompt.id) return;
    lastId.current = prompt.id;
    generation.current++;
    busy.current = false;
    setSending(false);
    setSettled(false);
    setFailure(null);
    // A new prompt must not inherit the text typed into the old one.
    if (input.current) input.current.value = '';
  }, [prompt.id]);

  // useDialogFocus captures the opener before it moves focus, so the first
  // focus belongs to it and a close can give the opener its focus back. From
  // then on this effect refocuses when a reconnect or a replaced prompt
  // leaves focus outside the dialog.
  const firstFocus = useRef(true);
  useLayoutEffect(() => {
    if (firstFocus.current) {
      firstFocus.current = false;
      return;
    }
    const el = root.current;
    if (!el) return;
    const active = document.activeElement;
    if (active instanceof HTMLElement && el.contains(active)) return;
    (input.current ?? primary.current ?? el).focus();
  }, [prompt.id, focusTick]);

  useEffect(() => {
    const recovered = wasDisabled.current && !disabled;
    wasDisabled.current = disabled;
    if (!recovered) return;
    // A reconnect re-arms a dialog whose removal was missed.
    setSettled(false);
    setFocusTick((n) => n + 1);
  }, [disabled]);

  // run guards one request at a time, and drops a result whose prompt was
  // replaced while the request was on its way.
  const run = async (call: () => Promise<Failure | null>) => {
    if (busy.current || settled || disabled) return;
    busy.current = true;
    setSending(true);
    setFailure(null);
    const gen = generation.current;
    const id = prompt.id;
    try {
      const err = await call();
      if (!alive.current || gen !== generation.current || currentId.current !== id) return;
      if (err) setFailure(err);
      else setSettled(true);
    } catch (err) {
      if (alive.current && gen === generation.current && currentId.current === id) setFailure(describeFailure(err));
    } finally {
      // A late answer to a prompt that is no longer here must not clear the
      // guard the current prompt is holding.
      if (gen === generation.current && currentId.current === id) {
        busy.current = false;
        setSending(false);
      }
    }
  };

  // answer sends the secret the field held; it is a local of the call, so
  // no state or ref keeps it.
  const answer = (secret: string) => run(() => actions.answerSecure(prompt, secret));

  // cancel dismisses the prompt once. A touch prompt is answered by the key
  // itself, so the browser never cancels one.
  const cancel = () => (touch ? undefined : run(() => actions.cancelSecure(prompt)));

  const submit = (e: Event) => {
    e.preventDefault();
    if (!input.current) return;
    if (busy.current || settled || disabled) return;
    const secret = input.current.value;
    // The field must not hold the secret a moment longer than the submit,
    // before the request settles.
    input.current.value = '';
    void answer(secret);
  };

  useEffect(() => {
    if (touch) return;
    const onKey = (e: KeyboardEvent) => {
      // A repeated or composing Escape must not send a second cancel.
      if (e.key !== 'Escape' || e.repeat || e.isComposing || e.keyCode === 229) return;
      e.preventDefault();
      void cancel();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [prompt.id, disabled, settled, touch]);

  return (
    <div
      ref={root}
      class="interrupt glass glass--alert enter"
      role="dialog"
      aria-modal="true"
      aria-labelledby="sp-title"
      aria-describedby="sp-desc"
      data-danger="high"
      tabIndex={-1}
    >
      <div class="interrupt__scroll">
        <header>
          <p class="hud-label">Secure prompt</p>
          <h2 id="sp-title" class="interrupt__title">{prompt.title}</h2>
        </header>
        <p id="sp-desc">{prompt.message}</p>
        {prompt.details && prompt.details.length > 0 && (
          <ul class="sp__details">
            {prompt.details.map((d) => <li key={d}>{d}</li>)}
          </ul>
        )}
        {alertText && (
          <p class="interrupt__error sp__alert" role="alert">
            <span class="sp__glyph" aria-hidden="true">✕</span> {alertText}
          </p>
        )}
        {touch ? (
          // The terminal answers a touch prompt itself; the browser only
          // shows it, and the server refuses an answer or a cancel.
          <p class="sp__notice" role="status">Waiting for the terminal.</p>
        ) : secretLabel ? (
          <form class="sp__form" onSubmit={submit}>
            <label class="sp__field" for="sp-secret">
              <span class="hud-label">{secretLabel}</span>
              <input
                ref={input}
                id="sp-secret"
                class="sp__input"
                type="password"
                autocomplete="off"
                spellcheck={false}
                readOnly={sending || settled}
                aria-disabled={locked ? 'true' : undefined}
              />
            </label>
            <div class="sp__actions">
              <button ref={primary} type="submit" class="btn btn--primary" aria-disabled={locked ? 'true' : undefined} aria-busy={sending}>Unlock</button>
              <button type="button" class="btn btn--ghost" aria-disabled={locked ? 'true' : undefined} onClick={() => void cancel()}>Cancel</button>
            </div>
          </form>
        ) : (
          <div class="sp__actions">
            <button ref={primary} type="button" class="btn btn--primary" aria-disabled={locked ? 'true' : undefined} aria-busy={sending} onClick={() => void answer('')}>Confirm</button>
            <button type="button" class="btn btn--ghost" aria-disabled={locked ? 'true' : undefined} onClick={() => void cancel()}>Decline</button>
          </div>
        )}
        {disabled && <p class="sp__status" role="status">Reconnecting to crush</p>}
      </div>
    </div>
  );
}
