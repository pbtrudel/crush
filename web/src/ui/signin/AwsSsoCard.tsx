import { actions, state } from '../../state/store';
import { webAddress } from '../webAddress';

// AwsSsoCard follows an AWS SSO refresh the terminal runs. A run waits on
// it, so the card is amber, and it never takes the focus: the terminal is
// the one being asked.
export function AwsSsoCard({ inert }: { inert: boolean }) {
  const card = state.value.awsSso;
  if (card === null) return null;
  const href = card.url ? webAddress(card.url) : null;
  return (
    <section class="aws-sso glass" aria-labelledby="aws-sso-title" inert={inert}>
      <h2 id="aws-sso-title" class="aws-sso__title">AWS sign-in</h2>
      {card.command !== '' && (
        <p class="aws-sso__command"><code>{card.command}</code></p>
      )}
      {/* The region is always there, so the URL frame that fills it in
          later is announced. */}
      <p class="aws-sso__url" aria-live="polite">
        {card.url === undefined ? null : href !== null
          ? <a href={href} target="_blank" rel="noopener noreferrer">Open the AWS sign-in page</a>
          : card.url}
      </p>
      {card.error !== undefined ? (
        <div class="aws-sso__error" role="alert">
          <span>{card.error}</span>
          <button type="button" class="btn btn--ghost" onClick={() => actions.dismissAwsSso()}>Dismiss</button>
        </div>
      ) : (
        <p class="aws-sso__status" role="status">Waiting for the AWS CLI to finish</p>
      )}
    </section>
  );
}
