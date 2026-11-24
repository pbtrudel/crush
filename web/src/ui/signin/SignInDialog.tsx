import { useEffect, useRef, useState } from 'preact/hooks';
import type { SignInResult, SignInState, SignInTarget } from '../../api/types';
import { api } from '../../api/http';
import { describeFailure, type Failure } from '../../state/failure';
import { openModels } from '../../state/palette';
import { applySignIn, pushToast } from '../../state/reducer';
import { closeSignIn, signInModel, signInOpen } from '../../state/signin';
import { actions, dispatch, state } from '../../state/store';
import { useDialogFocus, usePaletteDeckFocus } from '../interrupt/focus';
import { webAddress } from '../webAddress';

const NO_WEB_ADDRESS = 'The provider did not return a web address';

// The sentence a refused model switch adds after the sign-in.
const MODEL_REASONS: Record<string, string> = {
  agent_busy: 'Crush is busy; choose the model when it finishes.',
  agent_starting: 'Finish setting up Crush in the terminal, then choose a model.',
  model_unavailable: 'Choose a model your account offers.',
  model_failed: 'Choose a model from the selector.',
};

type Method = 'oauth' | 'api_key';

function methodWord(method: string): string {
  return method === 'api_key' ? 'API key' : 'Account login';
}

// SignInDialog is the glass panel a tab signs in to a provider from: the
// provider list, the key form and the one OAuth flow. It is plain glass with
// cyan edges: the user opened it, it is not a request that needs you.
export function SignInDialog({ inert }: { inert: boolean }) {
  const request = signInOpen.value;
  if (request === null) return null;
  return <SignInPanel key={`${request.provider ?? ''}:${request.model?.type ?? ''}:${request.model?.model ?? ''}`} provider={request.provider} inert={inert} />;
}

function SignInPanel({ provider, inert }: { provider?: string; inert: boolean }) {
  const s = state.value;
  const modelTarget = signInModel();
  const [targets, setTargets] = useState<SignInTarget[] | null>(null);
  const [loadError, setLoadError] = useState<unknown | null>(null);
  const [selected, setSelected] = useState<string | null>(provider ?? null);
  const [method, setMethod] = useState<Method>('oauth');
  const [query, setQuery] = useState('');
  const [failure, setFailure] = useState<Failure | null>(null);
  const [sending, setSending] = useState(false);
  const [tick, setTick] = useState(() => Date.now());
  const dialog = useRef<HTMLDivElement>(null);
  const filter = useRef<HTMLInputElement>(null);
  const keyInput = useRef<HTMLInputElement>(null);
  const codeInput = useRef<HTMLInputElement>(null);
  const codeValue = useRef<HTMLElement>(null);
  const busy = useRef(false);
  const alive = useRef(true);
  // A selection or a replaced flow invalidates the request in flight, so a
  // late answer never lands in the panel of another provider.
  const generation = useRef(0);

  useDialogFocus(dialog, 'signin');
  usePaletteDeckFocus();

  useEffect(() => {
    alive.current = true;
    return () => { alive.current = false; };
  }, []);

  // One request for the targets and one for the flow a reload or the
  // terminal started, both dropped if the dialog closed or reloaded.
  const load = async (): Promise<void> => {
    const gen = ++generation.current;
    try {
      const [{ providers }, status] = await Promise.all([
        api.signInTargets(),
        api.signInStatus().catch(() => null),
      ]);
      if (!alive.current || gen !== generation.current) return;
      setTargets(providers);
      setLoadError(null);
      // A frame or the snapshot is newer than this answer, so status only
      // fills in a flow the tab never saw.
      const current = state.value.signin;
      if (status && current === null) dispatch((next) => applySignIn(next, status));
      setSelected((selectedId) => selectedId ?? current?.provider ?? status?.provider ?? null);
    } catch (err) {
      if (!alive.current || gen !== generation.current) return;
      setLoadError(err);
    }
  };

  useEffect(() => { void load(); }, []);

  // Preact drops a false spellcheck attribute, so it is written back onto
  // the fields: a secret must never offer a spelling suggestion.
  useEffect(() => {
    keyInput.current?.setAttribute('spellcheck', 'false');
    codeInput.current?.setAttribute('spellcheck', 'false');
  });

  const flow = s.signin && s.signin.provider === selected ? s.signin : null;
  // A running flow names a provider the list may not carry, such as one the
  // terminal started: the flow itself is enough to show its panel.
  const listed = targets?.find((t) => t.id === selected) ?? null;
  const target: SignInTarget | null = listed ?? (selected !== null && flow !== null
    ? { id: selected, name: flow.provider_name || selected, configured: true, methods: ['oauth'], oauth_kind: flow.kind }
    : null);

  // The provider's first method is its default, so the panel opens on the
  // one the gateway named first.
  useEffect(() => {
    if (!target) return;
    setMethod(target.methods[0] === 'api_key' ? 'api_key' : 'oauth');
  }, [selected, targets]);

  const live = flow !== null && (flow.state === 'waiting' || flow.state === 'saving');
  const expiresAt = live && flow.state === 'waiting' ? flow.expires_at : undefined;

  // The code's remaining minutes are shown from the same clock the status
  // line holds, so a long wait does not keep an old number on screen.
  useEffect(() => {
    if (!expiresAt) return;
    const timer = window.setInterval(() => setTick(Date.now()), 60_000);
    return () => window.clearInterval(timer);
  }, [expiresAt]);

  // run guards one request at a time. A result whose provider or flow was
  // replaced while the request was on its way is dropped.
  const run = async (call: () => Promise<void>): Promise<void> => {
    if (busy.current || inert) return;
    busy.current = true;
    setSending(true);
    setFailure(null);
    const gen = generation.current;
    try {
      await call();
    } catch (err) {
      if (alive.current && gen === generation.current) setFailure(describeFailure(err));
    } finally {
      if (gen === generation.current) {
        busy.current = false;
        setSending(false);
      }
    }
  };

  const select = (id: string) => {
    if (id === selected) return;
    generation.current++;
    busy.current = false;
    setSending(false);
    setFailure(null);
    setSelected(id);
    // A new provider must not inherit the text typed for the last one.
    if (keyInput.current) keyInput.current.value = '';
    if (codeInput.current) codeInput.current.value = '';
  };

  const providerName = target?.name || target?.id || 'the provider';

  // The model reason is pushed only when a model was asked for: a plain
  // sign-in answers model_applied false with no reason.
  const finish = (result: SignInResult) => {
    closeSignIn();
    dispatch((next) => pushToast(next, 'info', `Signed in to ${providerName}.`));
    if (result.model_applied || !result.reason) return;
    dispatch((next) => pushToast(next, 'info', MODEL_REASONS[result.reason!] ?? MODEL_REASONS.model_failed));
    openModels();
  };

  const submitKey = (e: Event) => {
    e.preventDefault();
    if (busy.current || inert || !target) return;
    const input = keyInput.current;
    if (!input) return;
    const key = input.value;
    // The field must not hold the key a moment longer than the submit,
    // before the request settles.
    input.value = '';
    // A provider picked while the key is checked keeps the panel open: the
    // late result belongs to the one the user left.
    const gen = generation.current;
    void run(async () => {
      const result = await actions.signInKey(target.id, key, modelTarget);
      if (!alive.current || gen !== generation.current) return;
      finish(result);
    });
  };

  const start = () => {
    if (!target) return;
    void run(async () => {
      await actions.signInStart(target.id, modelTarget);
    });
  };

  const cancel = () => {
    if (!flow) return;
    void run(async () => {
      await actions.signInCancel(flow.flow_id);
      // The list is where the user chooses the next thing to try.
      document.getElementById(`signin-option-${selected}`)?.focus();
    });
  };

  const submitCode = (e: Event) => {
    e.preventDefault();
    if (busy.current || inert || !flow) return;
    const input = codeInput.current;
    if (!input) return;
    const code = input.value;
    input.value = '';
    void run(async () => {
      await actions.signInCode(flow.flow_id, code);
    });
  };

  const selectCode = () => {
    const el = codeValue.current;
    const selection = window.getSelection();
    if (!el || !selection) return;
    const range = document.createRange();
    range.selectNodeContents(el);
    selection.removeAllRanges();
    selection.addRange(range);
  };

  // A copy the browser refuses keeps the code usable: it is selected so the
  // user can copy it by hand.
  const copyCode = () => {
    const code = flow?.user_code ?? '';
    if (!navigator.clipboard?.writeText) {
      selectCode();
      return;
    }
    navigator.clipboard.writeText(code).catch(selectCode);
  };

  const needle = query.trim().toLowerCase();
  const filtered = (targets ?? []).filter((t) => needle === ''
    || [t.name, t.id].some((text) => text.toLowerCase().includes(needle)));
  const groups: { label: string; items: SignInTarget[] }[] = [
    { label: 'Signed in', items: filtered.filter((t) => t.configured) },
    { label: 'Available', items: filtered.filter((t) => !t.configured) },
  ].filter((group) => group.items.length > 0);

  const optionId = (id: string) => `signin-option-${id}`;

  const focusOption = (index: number) => {
    const items = groups.flatMap((g) => g.items);
    if (items.length === 0) return;
    const to = ((index % items.length) + items.length) % items.length;
    document.getElementById(optionId(items[to].id))?.focus();
  };

  const locked = inert || sending;
  const alertText = failure?.text ?? null;
  const href = flow?.verification_url ? webAddress(flow.verification_url) : null;
  const minutes = expiresAt ? Math.max(1, Math.ceil((expiresAt - tick / 1000) / 60)) : 0;
  const shownLoad = loadError === null ? null : describeFailure(loadError);

  const option = (t: SignInTarget) => (
    <div
      key={t.id}
      id={optionId(t.id)}
      class="signin__option"
      role="option"
      aria-selected={selected === t.id ? 'true' : 'false'}
      tabIndex={selected === t.id ? 0 : -1}
      onClick={() => select(t.id)}
      onKeyDown={(e) => {
        if (e.altKey || e.ctrlKey || e.metaKey) return;
        if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
          e.preventDefault();
          const index = filtered.indexOf(t);
          focusOption(index + (e.key === 'ArrowDown' ? 1 : -1));
          return;
        }
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          select(t.id);
        }
      }}
    >
      <span class="signin__name">{t.name || t.id}</span>
      {t.configured && <span class="signin__tag hud-label">signed in</span>}
    </div>
  );

  const keyForm = (t: SignInTarget) => (
    <form class="signin__form" onSubmit={submitKey}>
      <label class="signin__field" for={`signin-key-${t.id}`}>
        <span class="hud-label">API key</span>
        <input
          ref={keyInput}
          id={`signin-key-${t.id}`}
          class="signin__input"
          type="password"
          autocomplete="off"
          spellcheck={false}
          aria-disabled={locked ? 'true' : undefined}
        />
      </label>
      <p class="signin__note">The key is sent once and never kept in this tab.</p>
      <div class="signin__actions">
        <button type="submit" class="btn btn--primary" aria-disabled={locked ? 'true' : undefined} aria-busy={sending}>Sign in</button>
      </div>
    </form>
  );

  const signInLink = (name: string) => (href !== null ? (
    <p class="signin__link">
      <a href={href} target="_blank" rel="noopener noreferrer">Open the {name} sign-in page</a>
    </p>
  ) : (
    <p class="signin__alert" role="alert">
      <span aria-hidden="true">✕</span> {NO_WEB_ADDRESS}
    </p>
  ));

  const waiting = (t: SignInTarget, current: SignInState) => (
    <>
      {current.kind === 'device' && current.user_code && (
        <div class="signin__code">
          <code class="signin__code-value" ref={codeValue}>{current.user_code}</code>
          <button type="button" class="signin__action" onClick={copyCode}>Copy code</button>
        </div>
      )}
      {signInLink(t.name || t.id)}
      {current.code_entry && (
        <form class="signin__form" onSubmit={submitCode}>
          <label class="signin__field" for="signin-code">
            <span class="hud-label">Code from the sign-in page</span>
            <input
              ref={codeInput}
              id="signin-code"
              class="signin__input"
              type="password"
              autocomplete="off"
              spellcheck={false}
              aria-disabled={locked ? 'true' : undefined}
            />
          </label>
          <div class="signin__actions">
            <button type="submit" class="btn btn--primary" aria-disabled={locked ? 'true' : undefined} aria-busy={sending}>Continue</button>
          </div>
        </form>
      )}
      <p class="signin__status" role="status">
        {current.state === 'saving' ? 'Saving the sign-in' : `Waiting for ${t.name || t.id}`}
        {current.state === 'waiting' && minutes > 0 && ` · Code expires in ${minutes} ${minutes === 1 ? 'minute' : 'minutes'}`}
      </p>
      {current.state === 'waiting' && (
        <div class="signin__actions">
          <button type="button" class="btn btn--ghost" aria-disabled={locked ? 'true' : undefined} onClick={() => void cancel()}>Cancel sign-in</button>
        </div>
      )}
    </>
  );

  const finished = (t: SignInTarget, current: SignInState) => {
    const name = current.provider_name || t.name || t.id;
    if (current.state === 'done') {
      return (
        <>
          <p class="signin__done" role="status">Signed in to {name}.</p>
          <div class="signin__actions">
            <button type="button" class="btn btn--primary" onClick={() => { closeSignIn(); openModels(); }}>Choose a model</button>
            <button type="button" class="btn btn--ghost" onClick={() => closeSignIn()}>Close</button>
          </div>
        </>
      );
    }
    const sentence = current.state === 'cancelled' ? 'Sign-in cancelled.' : current.error || 'The sign-in did not finish. Try again.';
    return (
      <>
        <p class="signin__alert" role="alert">
          <span aria-hidden="true">✕</span> {sentence}
        </p>
        <div class="signin__actions">
          <button type="button" class="btn btn--primary" aria-disabled={locked ? 'true' : undefined} onClick={start}>Try again</button>
        </div>
      </>
    );
  };

  const oauthPanel = (t: SignInTarget) => (
    <>
      {alertText && (
        <p class="signin__alert" role="alert">
          <span aria-hidden="true">✕</span> {alertText}
        </p>
      )}
      {flow !== null && (flow.state === 'waiting' || flow.state === 'saving')
        ? waiting(t, flow)
        : flow !== null && (flow.state === 'done' || flow.state === 'error' || flow.state === 'cancelled')
          ? finished(t, flow)
          : (
            <div class="signin__actions">
              <button type="button" class="btn btn--primary" aria-disabled={locked ? 'true' : undefined} aria-busy={sending} onClick={start}>Start sign-in</button>
            </div>
          )}
    </>
  );

  const methods = target?.methods ?? [];
  // A provider with one method has no radio group, so the panel follows it
  // from the first render rather than a frame later.
  const shownMethod: Method = methods.length > 1 ? method : methods[0] === 'api_key' ? 'api_key' : 'oauth';

  return (
    <div class="signin-layer" inert={inert}>
      <div
        class="signin glass scanlines enter"
        role="dialog"
        aria-modal="true"
        aria-label="Sign in"
        ref={dialog}
        tabIndex={-1}
      >
        <header class="signin__head">
          <p class="hud-label">Provider sign-in</p>
          <h2 class="signin__title">Sign in to a provider</h2>
        </header>
        {shownLoad ? (
          <div class="signin__status" role="alert">
            <span>{shownLoad.text}</span>
            <button type="button" class="link-button" onClick={() => void load()}>Retry</button>
          </div>
        ) : (
          <>
            <input
              ref={filter}
              class="signin__filter"
              aria-label="Filter providers"
              placeholder="Filter providers"
              value={query}
              onInput={(e) => setQuery((e.currentTarget as HTMLInputElement).value)}
              onKeyDown={(e) => {
                if (e.key !== 'ArrowDown' || e.altKey || e.ctrlKey || e.metaKey) return;
                e.preventDefault();
                focusOption(0);
              }}
            />
            {targets === null ? (
              <p class="signin__status" role="status">Loading providers</p>
            ) : (
              <div class="signin__list" role="listbox" aria-label="Providers" aria-busy={sending ? 'true' : undefined}>
                {groups.map((group) => (
                  <div key={group.label} class="signin__group" role="group" aria-label={group.label}>
                    <h3 class="signin__group-title" aria-hidden="true">{group.label}</h3>
                    {group.items.map(option)}
                  </div>
                ))}
              </div>
            )}
            {targets !== null && groups.length === 0 && <p class="signin__empty">No providers match</p>}
            {target !== null && (
              <section class="signin__panel" aria-label={`Sign in to ${target.name || target.id}`}>
                {methods.length > 1 ? (
                  <fieldset class="signin__methods">
                    <legend class="hud-label">Sign in with</legend>
                    {methods.map((m) => (
                      <label key={m} class="signin__method">
                        <input
                          type="radio"
                          name="signin-method"
                          value={m}
                          checked={method === m}
                          onChange={() => setMethod(m === 'api_key' ? 'api_key' : 'oauth')}
                        />
                        {methodWord(m)}
                      </label>
                    ))}
                  </fieldset>
                ) : null}
                {shownMethod === 'api_key' && methods.includes('api_key') ? (
                  <>
                    {alertText && (
                      <p class="signin__alert" role="alert">
                        <span aria-hidden="true">✕</span> {alertText}
                      </p>
                    )}
                    {keyForm(target)}
                  </>
                ) : (
                  oauthPanel(target)
                )}
              </section>
            )}
          </>
        )}
      </div>
    </div>
  );
}

