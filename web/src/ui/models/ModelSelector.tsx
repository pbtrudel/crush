import { useEffect, useRef, useState } from 'preact/hooks';
import type { ModelChoice } from '../../api/types';
import { ApiError, api } from '../../api/http';
import { STARTING_TEXT, describeFailure } from '../../state/failure';
import { showThinkingToggle } from '../../state/model';
import { state } from '../../state/store';
import { closeModels, modelsFocus, modelsOpen } from '../../state/palette';
import { useDialogFocus, usePaletteDeckFocus } from '../interrupt/focus';

const BUSY_TEXT = 'Crush is busy. Try again when the turn ends.';

// The busier the agent, the shorter the wait: a refused switch names the
// turn that has to end, every other failure keeps its own description.
function describeModelFailure(err: unknown): ReturnType<typeof describeFailure> {
  if (err instanceof ApiError && err.code === 'agent_busy') return { tone: 'error', text: BUSY_TEXT };
  return describeFailure(err);
}

// formatContext shortens a context window for the choice rows: thousands
// below the point where they would read 1000k, at most one decimal above
// it. Zero or a missing window shows nothing.
export function formatContext(window: number | undefined): string {
  if (!window || window < 0) return '';
  if (window < 999_500) return `${Math.round(window / 1_000)}k`;
  const millions = Math.round(window / 100_000) / 10;
  return `${Number.isInteger(millions) ? millions.toFixed(0) : millions.toFixed(1)}M`;
}

type ModelType = 'main' | 'small';

export function ModelSelector({ inert }: { inert: boolean }) {
  if (!modelsOpen.value) return null;
  return <ModelsDialog inert={inert} />;
}

function ModelsDialog({ inert }: { inert: boolean }) {
  const s = state.value;
  const model = s.model;
  const [type, setType] = useState<ModelType>('main');
  const [choices, setChoices] = useState<ModelChoice[]>([]);
  const [query, setQuery] = useState('');
  const [active, setActive] = useState(0);
  const [loadError, setLoadError] = useState<unknown | null>(null);
  const [actionError, setActionError] = useState<unknown | null>(null);
  // The effort the user picked, held until the model frame answers it or a
  // failure drops it: a native radio would otherwise stay on a lost choice.
  const [effortDraft, setEffortDraft] = useState<string | null>(null);
  const [reasoningBusy, setReasoningBusy] = useState(false);
  // The choice a starting agent refused, so its Retry can pick it again.
  const [retryChoice, setRetryChoice] = useState<ModelChoice | null>(null);
  // The list is busy while the pick is in flight, so a screen reader hears
  // that the answer has not landed yet.
  const [selectingBusy, setSelectingBusy] = useState(false);
  const dialog = useRef<HTMLDivElement>(null);
  const filterInput = useRef<HTMLInputElement>(null);
  const alive = useRef(true);
  const generation = useRef(0);
  const selecting = useRef(false);
  const reasoningRef = useRef(false);
  // A retry that succeeds hands focus to the filter, but the button it
  // replaces only leaves the DOM a render later.
  const focusRequest = useRef<'filter' | null>(null);

  useDialogFocus(dialog, 'models');
  usePaletteDeckFocus();

  useEffect(() => {
    alive.current = true;
    return () => { alive.current = false; };
  }, []);

  // The opener decides where the first focus lands: the control the palette
  // named, or the filter a switching user wants to type in.
  useEffect(() => {
    if (modelsFocus.value === 'reasoning') {
      const control = dialog.current?.querySelector<HTMLElement>('input[name="model-effort"]:checked, [role="switch"]');
      if (control) {
        control.focus();
        return;
      }
    }
    filterInput.current?.focus();
  }, []);

  const load = async (retry = false): Promise<boolean> => {
    const gen = ++generation.current;
    try {
      const { choices: list } = await api.providers();
      if (!alive.current || gen !== generation.current) return false;
      setChoices(list);
      setLoadError(null);
      // A fresh list means the failure it showed is no longer true.
      setActionError(null);
      if (retry) focusRequest.current = 'filter';
      return true;
    } catch (err) {
      if (!alive.current || gen !== generation.current) return false;
      setLoadError(err);
      return false;
    }
  };

  useEffect(() => { void load(); }, []);

  // An answer can arrive after the dialog closed; the flag keeps it from
  // reaching an unmounted tree, and the counter drops a slower older one.
  useEffect(() => {
    const request = focusRequest.current;
    if (request === null) return;
    focusRequest.current = null;
    filterInput.current?.focus();
  });

  const levels = model.effort_levels ?? [];
  // One source for the fieldset and for the focus fallback: whichever
  // control the fieldset renders, a frame that swaps or removes it can
  // leave a focused control unmounted.
  const reasoningKind: 'levels' | 'switch' | 'none' = levels.length > 0
    ? 'levels'
    : showThinkingToggle(model) ? 'switch' : 'none';
  const checkedEffort = effortDraft ?? model.reasoning_effort ?? '';

  // The frame is the model state: a settled effort clears the draft, and a
  // model whose control just changed hands focus to the filter when the
  // focused control left the tree with it.
  const kindBefore = useRef(reasoningKind);
  useEffect(() => { setEffortDraft(null); }, [model.reasoning_effort]);
  useEffect(() => {
    const was = kindBefore.current;
    kindBefore.current = reasoningKind;
    if (was === reasoningKind) return;
    const focused = document.activeElement;
    if (focused !== null && dialog.current?.contains(focused)) return;
    filterInput.current?.focus();
  }, [reasoningKind]);

  const currentRef = type === 'main'
    ? { provider: model.provider, id: model.id }
    : model.small;
  const isCurrent = (c: ModelChoice) => !!currentRef && c.provider === currentRef.provider && c.id === currentRef.id;

  const needle = query.trim().toLowerCase();
  const list = needle === ''
    ? choices
    : choices.filter((c) => [c.name, c.id, c.provider, c.provider_name]
      .some((text) => text.toLowerCase().includes(needle)));

  // The current model of the chosen type owns the tab stop until an arrow
  // moves it, so Tab reaches the list where the user is looking.
  useEffect(() => {
    const index = list.findIndex(isCurrent);
    setActive(index >= 0 ? index : 0);
    // The list identity changes with its sources, not with each render.
  }, [type, query, choices]);

  const currentIndex = list.length === 0 ? 0 : Math.min(active, list.length - 1);
  const optionId = (c: ModelChoice) => `model-option-${c.provider}-${c.id}`;

  const focusOption = (index: number) => {
    if (list.length === 0) return;
    const to = ((index % list.length) + list.length) % list.length;
    setActive(to);
    // The list scrolls under the HUD, so bring the whole row into view.
    // jsdom has no scrollIntoView, hence the optional call.
    const el = document.getElementById(optionId(list[to]));
    el?.focus();
    el?.scrollIntoView?.({ block: 'nearest' });
  };

  const choose = async (c: ModelChoice) => {
    if (selecting.current) return;
    setActionError(null);
    setRetryChoice(null);
    if (isCurrent(c)) {
      closeModels();
      return;
    }
    selecting.current = true;
    setSelectingBusy(true);
    try {
      await api.selectModel(type, c.provider, c.id);
    } catch (err) {
      if (!alive.current) return;
      if (describeModelFailure(err).tone === 'starting') setRetryChoice(c);
      setActionError(err);
      return;
    } finally {
      selecting.current = false;
      setSelectingBusy(false);
    }
    if (!alive.current) return;
    closeModels();
  };

  // A radio group moves in the DOM before any handler runs, so an ignored
  // change has to put the checked radio back itself.
  const restoreEffort = () => {
    dialog.current?.querySelectorAll<HTMLInputElement>('input[name="model-effort"]')
      .forEach((input) => { input.checked = input.value === checkedEffort; });
  };

  const changeEffort = async (effort: string) => {
    if (reasoningRef.current) {
      restoreEffort();
      return;
    }
    reasoningRef.current = true;
    setActionError(null);
    setRetryChoice(null);
    setEffortDraft(effort);
    setReasoningBusy(true);
    try {
      await api.setReasoning({ effort });
    } catch (err) {
      if (!alive.current) return;
      // The draft clears below either way; this names what went wrong.
      setActionError(err);
    } finally {
      reasoningRef.current = false;
      if (alive.current) {
        setReasoningBusy(false);
        setEffortDraft(null);
      }
    }
  };

  const changeThinking = async (think: boolean) => {
    if (reasoningRef.current) return;
    reasoningRef.current = true;
    setActionError(null);
    setRetryChoice(null);
    setReasoningBusy(true);
    try {
      await api.setReasoning({ think });
    } catch (err) {
      if (!alive.current) return;
      setActionError(err);
    } finally {
      reasoningRef.current = false;
      if (alive.current) setReasoningBusy(false);
    }
  };

  // The choices arrive grouped in provider order, so one pass keeps them in
  // the order the server sent.
  const groups: { provider: string; name: string; choices: ModelChoice[] }[] = [];
  for (const c of list) {
    const last = groups[groups.length - 1];
    if (last && last.provider === c.provider) last.choices.push(c);
    else groups.push({ provider: c.provider, name: c.provider_name || c.provider, choices: [c] });
  }

  const shownAction = actionError === null ? null : describeModelFailure(actionError);
  const shownLoad = loadError === null ? null : describeFailure(loadError);

  return (
    <div class="models-layer" inert={inert}>
      <div
        class="models glass scanlines enter"
        role="dialog"
        aria-modal="true"
        aria-label="Models"
        ref={dialog}
        tabIndex={-1}
      >
        {!s.agentReady && (
          <div class="models__status" role="status" data-tone="starting">
            <span>{STARTING_TEXT}</span>
          </div>
        )}
        <fieldset class="models__fieldset">
          <legend class="models__legend">Model type</legend>
          <div class="models__radios">
            <label class="models__radio">
              <input
                type="radio"
                name="model-type"
                value="main"
                checked={type === 'main'}
                onChange={() => setType('main')}
              />
              Large
            </label>
            <label class="models__radio">
              <input
                type="radio"
                name="model-type"
                value="small"
                checked={type === 'small'}
                onChange={() => setType('small')}
              />
              Small
            </label>
          </div>
        </fieldset>
        <input
          ref={filterInput}
          class="models__input"
          aria-label="Filter models"
          placeholder="Filter models"
          value={query}
          onInput={(e) => setQuery((e.currentTarget as HTMLInputElement).value)}
          onKeyDown={(e) => {
            if (e.key !== 'ArrowDown' || e.altKey || e.ctrlKey || e.metaKey) return;
            e.preventDefault();
            focusOption(currentIndex);
          }}
        />
        <div
          class="models__list"
          role="listbox"
          aria-label="Model choices"
          aria-busy={selectingBusy ? 'true' : undefined}
        >
          {groups.map((group) => {
            // A listbox may own only options and groups: the group carries
            // the provider name and the visible heading is decorative.
            return (
              <div key={group.provider} class="models__group" role="group" aria-label={group.name}>
                <h3 class="models__provider" aria-hidden="true">{group.name}</h3>
                {group.choices.map((c) => {
                  const index = list.indexOf(c);
                  const current = isCurrent(c);
                  const context = formatContext(c.context_window);
                  return (
                    <div
                      key={`${c.provider}:${c.id}`}
                      id={optionId(c)}
                      class="models__option"
                      role="option"
                      aria-selected={current ? 'true' : 'false'}
                      tabIndex={index === currentIndex ? 0 : -1}
                      onClick={() => void choose(c)}
                      onKeyDown={(e) => {
                        if (e.altKey || e.ctrlKey || e.metaKey) return;
                        if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
                          e.preventDefault();
                          focusOption(index + (e.key === 'ArrowDown' ? 1 : -1));
                          return;
                        }
                        if (e.key === 'Enter' || e.key === ' ') {
                          e.preventDefault();
                          void choose(c);
                        }
                      }}
                    >
                      <span class="models__name">{c.name || c.id}</span>
                      {current && <span class="models__tag hud-label">current</span>}
                      {context !== '' && <span class="models__context">{context}</span>}
                    </div>
                  );
                })}
              </div>
            );
          })}
        </div>
        {list.length === 0 && <p class="models__empty">No models match</p>}
        <p class="models__more">More providers: sign in from the terminal</p>
        {reasoningKind !== 'none' ? (
          <fieldset
            class={`models__fieldset models__reasoning${reasoningBusy ? ' models__reasoning--busy' : ''}`}
            aria-busy={reasoningBusy ? 'true' : undefined}
            aria-describedby="models-reasoning-note"
          >
            <legend class="models__legend">Reasoning</legend>
            <p id="models-reasoning-note" class="models__note">Applies to the large model</p>
            {reasoningKind === 'levels' ? (
              <div class="models__radios">
                {levels.map((level) => (
                  <label key={level} class="models__radio">
                    <input
                      type="radio"
                      name="model-effort"
                      value={level}
                      checked={checkedEffort === level}
                      onChange={() => void changeEffort(level)}
                    />
                    {level.charAt(0).toUpperCase() + level.slice(1)}
                  </label>
                ))}
              </div>
            ) : (
              <div class="models__radios">
                <button
                  type="button"
                  role="switch"
                  class="models__switch"
                  aria-checked={model.think ? 'true' : 'false'}
                  onClick={() => void changeThinking(!model.think)}
                >Thinking</button>
              </div>
            )}
          </fieldset>
        ) : null}
        {/* The line at the top already says the agent is starting. */}
        {shownAction && !(shownAction.tone === 'starting' && !s.agentReady) && (
          <div class="models__status" role={shownAction.tone === 'starting' ? 'status' : 'alert'} data-tone={shownAction.tone}>
            <span>{shownAction.text}</span>
            {shownAction.tone === 'starting' && retryChoice !== null && (
              <button type="button" class="link-button" onClick={() => void choose(retryChoice)}>Retry</button>
            )}
          </div>
        )}
        {shownLoad && (
          <div class="models__status" role={shownLoad.tone === 'starting' ? 'status' : 'alert'} data-tone={shownLoad.tone}>
            <span>{shownLoad.text}</span>
            <button type="button" class="link-button" onClick={() => void load(true)}>Retry</button>
          </div>
        )}
      </div>
    </div>
  );
}
