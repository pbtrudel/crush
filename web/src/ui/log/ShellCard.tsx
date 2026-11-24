import { useEffect, useLayoutEffect, useRef, useState } from 'preact/hooks';
import { ApiError, api } from '../../api/http';
import { describeFailure } from '../../state/failure';
import type { ShellRun } from '../../state/reducer';
import { actions, state } from '../../state/store';

// A finished card stays long enough for the persisted message to take over;
// after that it is dropped even if the message never came.
export const SHELL_CARD_TTL_MS = 30_000;

const PIN_SLACK = 80;

function exitLabel(run: ShellRun): string {
  return run.exitCode === null ? 'Exit unknown' : `Exit ${run.exitCode}`;
}

// ShellCard renders every live shell run of the session, one card each, at
// the end of the log. The log's own shell_command rendering takes over once
// the run is persisted.
export function ShellCard() {
  const { shells, sessionId } = state.value;
  const runs = Object.entries(shells).filter(([, run]) => run.sessionId === sessionId);
  if (runs.length === 0) return null;
  return (
    <>
      {runs.map(([id, run]) => <RunCard key={id} id={id} run={run} />)}
    </>
  );
}

function RunCard({ id, run }: { id: string; run: ShellRun }) {
  const [cancelling, setCancelling] = useState(false);
  const [cancelFailure, setCancelFailure] = useState<string | null>(null);
  const inFlight = useRef(false);
  // Cancel can unmount the moment the run finishes, and a browser moves the
  // focus of a removed control to the body before a layout effect can look.
  // Read the holder while the button is still in the document, then hand the
  // focus to the card once the done render has committed.
  const cancelRef = useRef<HTMLButtonElement>(null);
  const focusCard = useRef(false);
  const card = useRef<HTMLElement>(null);
  const well = useRef<HTMLPreElement>(null);
  const pinned = useRef(true);
  if (run.done && cancelRef.current !== null && document.activeElement === cancelRef.current) {
    focusCard.current = true;
  }

  // The well shows the tail as output streams, until the reader scrolls up.
  useLayoutEffect(() => {
    const el = well.current;
    if (el && pinned.current) el.scrollTop = el.scrollHeight;
  }, [run.output]);

  // Only a Cancel that held the focus hands it to the card.
  useLayoutEffect(() => {
    if (!run.done || !focusCard.current) return;
    focusCard.current = false;
    card.current?.focus();
  }, [run.done]);

  // A done card waits for its persisted message, then drops itself.
  useEffect(() => {
    if (!run.done) return;
    const timer = window.setTimeout(() => actions.removeShell(id), SHELL_CARD_TTL_MS);
    return () => window.clearTimeout(timer);
  }, [run.done, id]);

  const cancel = async () => {
    if (inFlight.current || run.done) return;
    inFlight.current = true;
    setCancelling(true);
    try {
      await api.cancelShell(id);
      setCancelFailure(null);
    } catch (err) {
      // The gateway no longer holds the run: no done frame is coming, so a
      // late cancel takes the card with it.
      if (err instanceof ApiError && err.code === 'unknown_run') {
        // The gateway no longer holds the run, so no done frame will come
        // and the card has nothing left to show.
        actions.removeShell(id);
      } else {
        setCancelFailure(describeFailure(err).text);
      }
    } finally {
      inFlight.current = false;
      setCancelling(false);
    }
  };

  return (
    <article class="shell-card" data-done={run.done} tabIndex={-1} ref={card}>
      <header class="shell-card__head">
        <code class="shell-card__command">{run.command ? `$ ${run.command}` : 'Shell command'}</code>
        {!run.done && (
          <button
            type="button"
            class="btn btn--ghost shell-card__cancel"
            ref={cancelRef}
            aria-disabled={cancelling ? 'true' : undefined}
            aria-busy={cancelling}
            onClick={() => void cancel()}
          >Cancel</button>
        )}
        {/* One polite region from the start, so the end is announced when
            its text appears. The streaming output stays out of it. */}
        <span class="hud-label shell-card__exit" role="status">{run.done ? exitLabel(run) : ''}</span>
      </header>
      <pre
        class="shell-card__output"
        ref={well}
        tabIndex={0}
        aria-label="Shell output"
        onScroll={() => {
          const el = well.current;
          if (el) pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < PIN_SLACK;
        }}
      ><code>{run.output}</code></pre>
      {run.done && run.error && <p class="shell-card__failure" role="alert">{run.error}</p>}
      {cancelFailure && <p class="shell-card__failure" role="alert">{cancelFailure}</p>}
    </article>
  );
}
