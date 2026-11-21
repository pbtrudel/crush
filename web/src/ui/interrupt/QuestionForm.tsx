import { useEffect, useRef, useState } from 'preact/hooks';
import type { Question, QuestionRequest } from '../../api/types';
import { actions } from '../../state/store';
import { buildAnswers, isComplete, type Draft } from './answers';
import { useDialogFocus } from './focus';

const UNREACHABLE = 'The request did not reach crush';

function Field({ q, d, onChange }: { q: Question; d: Draft; onChange: (d: Draft) => void }) {
  const name = `q-${q.id}`;
  return (
    <fieldset class="qform__field">
      <legend class="interrupt__title">{q.question}</legend>
      {q.description && <p class="qform__desc">{q.description}</p>}
      {q.type === 'yes_no' && (
        <div class="qform__choices">
          {[true, false].map((v) => (
            <label key={String(v)} class="qform__choice">
              <input type="radio" name={name} checked={d.yes === v} onChange={() => onChange({ ...d, yes: v })} />
              {v ? 'Yes' : 'No'}
            </label>
          ))}
        </div>
      )}
      {(q.type === 'single_choice' || q.type === 'multi_choice') && (
        <div class="qform__choices">
          {q.choices?.map((c) => {
            const checked = d.selected?.includes(c.id) ?? false;
            const multi = q.type === 'multi_choice';
            return (
              <label key={c.id} class="qform__choice">
                <input
                  type={multi ? 'checkbox' : 'radio'}
                  name={name}
                  checked={checked}
                  onChange={() => onChange({
                    ...d,
                    selected: multi ? (checked ? d.selected!.filter((x) => x !== c.id) : [...(d.selected ?? []), c.id]) : [c.id],
                  })}
                />
                <span><strong>{c.label}</strong>{c.description && <span class="qform__desc"> {c.description}</span>}</span>
              </label>
            );
          })}
        </div>
      )}
      <label class="qform__text">
        <span class="hud-label">{q.type === 'free_text' ? 'Answer' : 'Or write your own'}</span>
        <textarea rows={2} value={d.text ?? ''} onInput={(e) => onChange({ ...d, text: (e.currentTarget as HTMLTextAreaElement).value })} />
      </label>
    </fieldset>
  );
}

export function QuestionForm({ req, disabled = false }: { req: QuestionRequest; disabled?: boolean }) {
  const [tab, setTab] = useState(0);
  const [draft, setDraft] = useState<Record<string, Draft>>({});
  const [sending, setSending] = useState(false);
  // A batch that was sent or skipped must not be sent twice while the
  // notification removing it is on the way.
  const [settled, setSettled] = useState(false);
  const [error, setError] = useState('');
  const root = useRef<HTMLDivElement>(null);
  // The sending state lags a render, so a ref blocks a second send while the
  // first answer is in flight.
  const busy = useRef(false);
  const cancelled = useRef(false);
  // Changing the focus key makes the focus hook pull focus back into the
  // dialog after a reconnect.
  const [focusTick, setFocusTick] = useState(0);
  const lastId = useRef(req.id);
  const wasDisabled = useRef(disabled);
  const multi = req.questions.length > 1;
  const confirmTab = multi ? req.questions.length : -1;
  // A smaller batch can replace a larger one while the form is open; clamp so
  // the stale tab cannot index past the new questions.
  const current = req.questions[Math.min(tab, req.questions.length - 1)];
  useDialogFocus(root, `${req.id}:${tab}:${focusTick}`);

  useEffect(() => {
    if (lastId.current === req.id) return;
    lastId.current = req.id;
    busy.current = false;
    cancelled.current = false;
    setSending(false);
    setSettled(false);
    setError('');
    setDraft({});
    setTab(0);
  }, [req.id]);

  useEffect(() => {
    const recovered = wasDisabled.current && !disabled;
    wasDisabled.current = disabled;
    if (!recovered) return;
    // A reconnect re-arms a form whose removal was missed, and focus goes
    // back inside instead of staying on the body.
    cancelled.current = false;
    setSettled(false);
    setFocusTick((n) => n + 1);
  }, [disabled]);

  const submit = async () => {
    if (busy.current || settled || disabled) return;
    const missing = req.questions.findIndex((q) => !isComplete(q, draft[q.id]));
    if (missing >= 0) {
      setTab(missing);
      setError(`Answer "${req.questions[missing].question}" before sending.`);
      return;
    }
    busy.current = true;
    setSending(true);
    setError('');
    try {
      const err = await actions.answer(req.id, buildAnswers(req.questions, draft));
      // A response for a batch that was already replaced is stale.
      if (lastId.current !== req.id) return;
      if (err) setError(err);
      else setSettled(true);
    } catch {
      setError(UNREACHABLE);
    } finally {
      busy.current = false;
      setSending(false);
    }
  };

  // cancel skips the batch once, whether it came from the key or the button.
  // A failure re-arms the form so a failed skip cannot wedge the dialog.
  const cancel = async () => {
    if (disabled || settled || busy.current || cancelled.current) return;
    cancelled.current = true;
    busy.current = true;
    setSending(true);
    setSettled(true);
    setError('');
    try {
      const err = await actions.cancelQuestion(req.id);
      if (lastId.current !== req.id) return;
      if (err) {
        cancelled.current = false;
        setSettled(false);
        setError(err);
      }
    } catch {
      if (lastId.current === req.id) {
        cancelled.current = false;
        setSettled(false);
        setError(UNREACHABLE);
      }
    } finally {
      busy.current = false;
      setSending(false);
    }
  };

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      // A repeated or composing Escape must not send a second cancel.
      if (e.key !== 'Escape' || e.repeat || e.isComposing || e.keyCode === 229) return;
      void cancel();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [req.id, disabled, settled]);

  const locked = disabled || sending || settled;
  return (
    <div
      ref={root}
      class="interrupt qform"
      role="dialog"
      aria-modal="true"
      aria-labelledby="q-title"
      tabIndex={-1}
    >
      <form class="qform__body" onSubmit={(e) => { e.preventDefault(); void submit(); }}>
        <p id="q-title" class="hud-label">The agent is asking</p>
        {multi && (
          <div class="qform__tabs" role="tablist">
            {req.questions.map((q, i) => (
              <button key={q.id} type="button" role="tab" aria-selected={tab === i} data-done={isComplete(q, draft[q.id])} onClick={() => setTab(i)}>
                {q.label || `Q${i + 1}`}
              </button>
            ))}
            <button type="button" role="tab" aria-selected={tab === confirmTab} onClick={() => setTab(confirmTab)}>Confirm</button>
          </div>
        )}
        {tab === confirmTab ? (
          <section class="qform__confirm">
            <h2 class="interrupt__title">{req.confirm_title || 'Send these answers?'}</h2>
            {req.confirm_description && <p>{req.confirm_description}</p>}
          </section>
        ) : current ? (
          <Field q={current} d={draft[current.id] ?? {}} onChange={(d) => setDraft({ ...draft, [current.id]: d })} />
        ) : null}
        {error && <p class="interrupt__error" role="alert">{error}</p>}
        <div class="interrupt__actions">
          {multi && tab !== confirmTab
            ? <button type="button" class="btn btn--primary" onClick={() => setTab(tab + 1)}>Next</button>
            : <button type="submit" class="btn btn--primary" disabled={locked} aria-busy={sending}>Send answers</button>}
          <button type="button" class="btn btn--ghost" disabled={locked} onClick={cancel}>Skip question</button>
        </div>
      </form>
    </div>
  );
}
