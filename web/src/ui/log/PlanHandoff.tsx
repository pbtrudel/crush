import { useEffect, useLayoutEffect, useRef, useState } from 'preact/hooks';
import { api } from '../../api/http';
import { describeFailure, type Failure } from '../../state/failure';
import { planPending } from '../../state/reducer';
import { actions, state } from '../../state/store';
import { focusDeck } from '../deck/modes';

type Handoff = 'code' | 'code_yolo';

// PlanHandoff is the end of the log while a ready plan waits for the reader.
// It is a card, not a dialog: it asks for the core look and the title, but
// leaving the focus in the log keeps working.
export function PlanHandoff() {
  const s = state.value;
  const pending = planPending(s);
  const card = useRef<HTMLElement>(null);
  const reviseButton = useRef<HTMLButtonElement>(null);
  const box = useRef<HTMLTextAreaElement>(null);
  // A request may answer after its card is gone, so every answer checks the
  // generation it started in before touching the card.
  const alive = useRef(true);
  const generation = useRef(0);
  const inFlight = useRef(false);
  const wasPending = useRef(pending);
  const leaving = useRef(false);
  const [busy, setBusy] = useState(false);
  const [revising, setRevising] = useState(false);
  const [feedback, setFeedback] = useState('');
  const [failure, setFailure] = useState<Failure | null>(null);
  const [retry, setRetry] = useState<Handoff | 'revise' | null>(null);

  useEffect(() => () => { alive.current = false; }, []);
  useEffect(() => {
    setRevising(false);
    setFeedback('');
    setFailure(null);
    setRetry(null);
  }, [s.sessionId]);
  // The card leaves when the plan clears; the next plan starts unfettered.
  useEffect(() => {
    if (pending) return;
    setBusy(false);
    setRevising(false);
    setFeedback('');
    setFailure(null);
    setRetry(null);
  }, [pending]);
  useEffect(() => {
    if (revising) box.current?.focus();
  }, [revising]);

  // A plan that clears invalidates the request still in flight, so a late
  // answer cannot put a failure on the next plan.
  if (wasPending.current && !pending) {
    generation.current++;
    inFlight.current = false;
  }
  wasPending.current = pending;

  // Preact removes the focused article before a layout effect can read the
  // focus, so decide from document.activeElement while the card still owns
  // it and act once the removal has committed.
  if (!pending && card.current?.contains(document.activeElement)) leaving.current = true;
  useLayoutEffect(() => {
    if (!leaving.current) return;
    leaving.current = false;
    focusDeck();
  }, [pending]);

  const start = async (action: Handoff) => {
    if (inFlight.current) return;
    const gen = generation.current;
    inFlight.current = true;
    setBusy(true);
    setFailure(null);
    setRetry(null);
    try {
      await api.planHandoff(s.sessionId, action);
      // The plan_ready frame clears the card, so a 202 stays busy until it
      // lands: a second handoff must not race the first.
      if (!alive.current || gen !== generation.current) return;
    } catch (err) {
      if (!alive.current || gen !== generation.current) return;
      inFlight.current = false;
      setBusy(false);
      const f = describeFailure(err);
      setFailure(f);
      if (f.tone === 'starting') setRetry(action);
    }
  };

  const sendRevision = async () => {
    const text = feedback.trim();
    if (!text || inFlight.current) return;
    const gen = generation.current;
    inFlight.current = true;
    setBusy(true);
    setFailure(null);
    setRetry(null);
    try {
      const failed = await actions.sendPrompt(text, []);
      if (!alive.current || gen !== generation.current) return;
      inFlight.current = false;
      setBusy(false);
      if (failed) {
        setFailure(failed);
        if (failed.tone === 'starting') setRetry('revise');
        return;
      }
      setFeedback('');
      setRevising(false);
      reviseButton.current?.focus();
    } catch {
      if (!alive.current || gen !== generation.current) return;
      inFlight.current = false;
      setBusy(false);
      setFailure({ tone: 'error', text: 'The feedback did not reach crush' });
    }
  };

  const cancelRevision = () => {
    setRevising(false);
    setFeedback('');
    setFailure(null);
    setRetry(null);
    reviseButton.current?.focus();
  };

  const again = () => {
    if (retry === 'code' || retry === 'code_yolo') void start(retry);
    else if (retry === 'revise') void sendRevision();
  };

  if (!pending) return null;
  const disabled = busy ? 'true' : undefined;
  return (
    <article class="plan-handoff glass glass--alert enter" ref={card} aria-labelledby="plan-handoff-title">
      <h2 id="plan-handoff-title" class="plan-handoff__title">The plan is ready</h2>
      <p class="plan-handoff__question">Ready to start coding?</p>
      <div class="plan-handoff__choices">
        <button
          type="button"
          class="btn btn--primary"
          data-action="code"
          aria-disabled={disabled}
          aria-busy={busy}
          onClick={() => void start('code')}
        >Start coding</button>
        <button
          type="button"
          class="btn"
          data-action="code_yolo"
          aria-disabled={disabled}
          aria-busy={busy}
          aria-describedby="plan-yolo-hint"
          onClick={() => void start('code_yolo')}
        >Code with YOLO</button>
        <button
          type="button"
          class="btn btn--ghost"
          data-action="revise"
          ref={reviseButton}
          aria-expanded={revising}
          aria-disabled={disabled}
          onClick={() => setRevising((v) => !v)}
        >Revise the plan</button>
      </div>
      <p id="plan-yolo-hint" class="hud-label plan-handoff__hint">YOLO mode skips permission prompts</p>
      {revising && (
        <form class="plan-handoff__revise" onSubmit={(e) => { e.preventDefault(); void sendRevision(); }}>
          <label class="hud-label" for="plan-feedback">What should change?</label>
          <textarea
            id="plan-feedback"
            ref={box}
            rows={3}
            value={feedback}
            onInput={(e) => setFeedback((e.currentTarget as HTMLTextAreaElement).value)}
          />
          <div class="plan-handoff__actions">
            <button type="submit" class="btn btn--primary" data-action="send" aria-disabled={disabled}>Send</button>
            <button type="button" class="btn btn--ghost" data-action="cancel" onClick={cancelRevision}>Cancel</button>
          </div>
        </form>
      )}
      {failure && (
        <div class="plan-handoff__failure">
          <p
            class="plan-handoff__error"
            data-tone={failure.tone}
            role={failure.tone === 'starting' ? 'status' : 'alert'}
          >{failure.text}</p>
          {failure.tone === 'starting' && retry && (
            <button type="button" class="btn btn--ghost" data-action="retry" onClick={again}>Retry</button>
          )}
        </div>
      )}
    </article>
  );
}
