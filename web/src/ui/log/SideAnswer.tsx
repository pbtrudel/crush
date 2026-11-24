import { actions, dismissSideAnswer, sideAnswer } from '../../state/store';
import { focusDeck } from '../deck/modes';
import { Markdown } from './Markdown';

// SideAnswer pins the answer to a side question above the deck. The
// exchange never joins the conversation, so the card says so, and it
// appears without taking the keyboard: the reader may still be typing.
export function SideAnswer() {
  const sa = sideAnswer.value;
  if (sa === null) return null;
  const error = sa.error;
  return (
    <aside class="side-answer glass enter" aria-label="Side answer">
      <h2 class="hud-label side-answer__title">Side answer, not added to the conversation</h2>
      <p class="side-answer__question">{sa.question}</p>
      {error !== null ? (
        <p
          class="side-answer__error"
          data-tone={error.tone}
          role={error.tone === 'starting' ? 'status' : 'alert'}
        >
          <span>{error.text}</span>
          {error.tone === 'starting' && (
            <button type="button" class="link-button" onClick={() => void actions.ask(sa.question)}>Retry</button>
          )}
        </p>
      ) : sa.answer === null ? (
        <p class="side-answer__pending">Asking...</p>
      ) : (
        <div class="side-answer__answer">
          <Markdown text={sa.answer} />
        </div>
      )}
      <p class="visually-hidden" role="status">{sa.answer === null ? '' : 'Side answer ready'}</p>
      <div class="side-answer__actions">
        <button
          type="button"
          class="btn btn--ghost"
          onClick={() => {
            dismissSideAnswer();
            focusDeck();
          }}
        >Dismiss</button>
      </div>
    </aside>
  );
}
