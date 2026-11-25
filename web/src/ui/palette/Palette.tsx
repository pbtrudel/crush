import { Fragment } from 'preact';
import { useEffect, useRef, useState } from 'preact/hooks';
import type { CommandEntry, McpPrompt, ModelChoice, Session, SkillEntry } from '../../api/types';
import { ApiError, api } from '../../api/http';
import { describeFailure } from '../../state/failure';
import { without } from '../../state/record';
import { adoptSession } from '../../state/reducer';
import { cinematic, cinematicAllowed, setCinematic } from '../../state/cinematic';
import { motion, setMotion, type Motion } from '../../state/motion';
import { openSignIn } from '../../state/signin';
import { toggleDrawer } from '../../state/drawer';
import { actions, dispatch, state } from '../../state/store';
import {
  argumentItem, closePalette, deckMode, openModels, openPalette, openSessions, openSystems, paletteOpen,
  paletteReturnFocus,
  paletteQuery, setPaletteQuery, type PaletteSection,
} from '../../state/palette';
import { useDialogFocus } from '../interrupt/focus';
import { attachFiles, shellFromPalette } from '../deck/CommandDeck';
import { focusDeck } from '../deck/modes';
import { ArgumentForm } from './ArgumentForm';
import {
  ACTIONS, buildItems, buildSkillToggles, filterItems, type PaletteItem, type PaletteSources,
} from './items';

interface Loaded {
  commands: CommandEntry[];
  prompts: McpPrompt[];
  skills: SkillEntry[];
  sessions: Pick<Session, 'id' | 'title'>[];
  models: ModelChoice[];
}

const EMPTY: Loaded = { commands: [], prompts: [], skills: [], sessions: [], models: [] };

// groupIn narrows a section to the groups it is about; the all section keeps
// every group in one list.
function groupIn(section: PaletteSection): string[] | null {
  switch (section) {
    case 'sessions': return ['Sessions'];
    case 'models': return ['Models'];
    case 'commands': return ['Commands', 'Skills', 'MCP prompts'];
    default: return null;
  }
}

function nextMotion(m: Motion): Motion {
  return m === 'full' ? 'reduced' : m === 'reduced' ? 'off' : 'full';
}

// Every group has its own source, so a refetch can name just the groups it
// needs and a failure only touches that group.
type SourceKey = 'commands' | 'prompts' | 'skills' | 'sessions' | 'models';

const SOURCE_KEYS: SourceKey[] = ['commands', 'prompts', 'skills', 'sessions', 'models'];

// FETCH reads one source, shaped like the Loaded field it belongs to.
const FETCH: { [K in SourceKey]: () => Promise<Loaded[K]> } = {
  commands: async () => (await api.commands()).commands,
  prompts: async () => (await api.mcpPrompts()).prompts,
  skills: async () => (await api.skills()).skills,
  sessions: async () => (await api.sessions()).map(({ id, title }) => ({ id, title })),
  models: async () => (await api.providers()).choices,
};

function merge<K extends SourceKey>(loaded: Loaded, key: K, value: Loaded[K]): Loaded {
  return { ...loaded, [key]: value };
}

// openDisplayMenu opens the HUD's Display menu, the surface the terminal's
// settings command reaches.
function openDisplayMenu(): void {
  const menu = document.querySelector<HTMLDetailsElement>('details.motion-menu');
  if (!menu) return;
  menu.open = true;
  const summary = menu.querySelector<HTMLElement>('summary');
  // The palette's focus trap hands the focus back to its opener as it
  // unmounts, so the menu takes it on the next microtask, after that
  // restore.
  queueMicrotask(() => summary?.focus());
}

export function Palette({ inert }: { inert: boolean }) {
  const section = paletteOpen.value;
  if (section === null) return null;
  return <PaletteDialog section={section} inert={inert} />;
}

function PaletteDialog({ section, inert }: { section: PaletteSection; inert: boolean }) {
  const s = state.value;
  const [loaded, setLoaded] = useState<Loaded>(EMPTY);
  const [errors, setErrors] = useState<Partial<Record<SourceKey, unknown>>>({});
  const [failure, setFailure] = useState<unknown | null>(null);
  // The item a starting agent refused, so its Retry can run it again.
  const [failed, setFailed] = useState<{ item: PaletteItem; values?: Record<string, string> } | null>(null);
  const [active, setActive] = useState(0);
  const dialog = useRef<HTMLDivElement>(null);
  const input = useRef<HTMLInputElement>(null);
  const alive = useRef(true);
  // A double Enter or a double click must not run the item twice; the state
  // alone lags a render, so a ref holds the in-flight guard.
  const running = useRef(false);
  const generations = useRef<Partial<Record<SourceKey, number>>>({});
  const argumentId = argumentItem.value;
  const query = paletteQuery.value;

  // The focus trap also restores the opener when the dialog unmounts, and
  // re-focuses the first control when the argument form comes and goes.
  useDialogFocus(dialog, `${section}:${argumentId ?? ''}`, inert);

  // A source can answer after the dialog closed; the flag keeps that answer
  // from reaching an unmounted tree.
  useEffect(() => {
    alive.current = true;
    return () => { alive.current = false; };
  }, []);

  // reload fetches the named sources (all by default) and merges each answer
  // into its own group. A failure keeps that group's previous value and
  // records the error, so one bad source never wipes the list.
  const reload = async (only?: SourceKey[]) => {
    await Promise.all((only ?? SOURCE_KEYS).map(async (key) => {
      // A slower answer from an earlier request must not overwrite a newer
      // one for the same source.
      const generation = (generations.current[key] ?? 0) + 1;
      generations.current[key] = generation;
      try {
        const value = await FETCH[key]();
        if (!alive.current || generations.current[key] !== generation) return;
        setLoaded((cur) => merge(cur, key, value));
        setErrors((cur) => without(cur, key));
      } catch (err) {
        if (!alive.current || generations.current[key] !== generation) return;
        setErrors((cur) => ({ ...cur, [key]: err }));
      }
    }));
  };

  const retry = async () => {
    // An action the starting agent refused keeps its item, so Retry repeats
    // that action; a failed source only needs its own fetch again.
    if (failed !== null) {
      const { item, values } = failed;
      // The Retry button leaves with the failure it replaces, so the list (or
      // the argument form, when the item had one) takes the focus first.
      (input.current ?? dialog.current?.querySelector<HTMLInputElement>('input'))?.focus();
      setFailed(null);
      await run(item, values);
      return;
    }
    await reload(SOURCE_KEYS.filter((key) => key in errors));
  };

  useEffect(() => {
    void reload();
  }, []);

  useEffect(() => {
    setActive(0);
  }, [query, section]);

  const sources: PaletteSources = {
    model: s.model,
    busy: s.busy,
    hasSession: !!s.sessionId,
    commands: loaded.commands,
    prompts: loaded.prompts,
    skills: loaded.skills,
    sessions: loaded.sessions,
    models: loaded.models,
    yolo: s.permissionMode,
    plan: s.inputMode,
    cinematic: cinematic.value,
    cinematicAllowed: cinematicAllowed(),
    motion: motion.value,
  };
  const scoped = section === 'skills' ? buildSkillToggles(loaded.skills) : buildItems(sources);
  const keep = groupIn(section);
  const inSection = keep === null ? scoped : scoped.filter((i) => keep.includes(i.group));
  const visible = filterItems(inSection, query);
  const argument = argumentId === null ? undefined : inSection.find((i) => i.id === argumentId);
  const current = visible.length === 0 ? -1 : Math.min(active, visible.length - 1);

  // Bring the keyboard selection into view as the arrows and the filter move
  // it, as the agent list does. jsdom has no scrollIntoView, hence the
  // optional call.
  useEffect(() => {
    if (current < 0) return;
    document.getElementById(`palette-option-${current}`)?.scrollIntoView?.({ block: 'nearest' });
  }, [current]);

  const sourceError = SOURCE_KEYS.find((key) => key in errors);
  const shownError = failure ?? (sourceError === undefined ? null : errors[sourceError]);
  const shown = shownError === null || shownError === undefined ? null : describeFailure(shownError);
  const canRetry = failed !== null || (failure === null && sourceError !== undefined);

  const runAction = async (action: string, values?: Record<string, string>) => {
    const sid = s.sessionId;
    switch (action) {
      case ACTIONS.newSession: {
        const created = await api.createSession();
        dispatch((cur) => adoptSession(cur, created.id));
        closePalette();
        return;
      }
      // These dialogs open as the palette leaves, so they mark themselves
      // for the deck rather than for a vanished opener.
      case ACTIONS.sessions: paletteReturnFocus.value = true; closePalette(); openSessions(); return;
      case ACTIONS.model: paletteReturnFocus.value = true; closePalette(); openModels(); return;
      case ACTIONS.reasoningLevel: paletteReturnFocus.value = true; closePalette(); openModels('reasoning'); return;
      case ACTIONS.systems: paletteReturnFocus.value = true; closePalette(); openSystems(); return;
      case ACTIONS.signIn: paletteReturnFocus.value = true; closePalette(); openSignIn(); return;
      case ACTIONS.compact:
        if (!sid) throw new ApiError(400, 'missing_session', 'There is no session to compact');
        await api.summarize(sid);
        closePalette();
        return;
      case ACTIONS.runShell: closePalette(); shellFromPalette(); return;
      case ACTIONS.addNote: deckMode.value = 'note'; closePalette(); queueMicrotask(focusDeck); return;
      case ACTIONS.askSide: {
        closePalette();
        void actions.ask((values?.question ?? '').trim());
        return;
      }
      case ACTIONS.reloadSkills: await api.reloadSkills(); closePalette(); return;
      case ACTIONS.toggleSkills: openPalette('skills'); return;
      case ACTIONS.drawer: toggleDrawer(); closePalette(); return;
      case ACTIONS.display: closePalette(); openDisplayMenu(); return;
      case ACTIONS.reloadConfig: await api.reloadConfig(); closePalette(); return;
      case ACTIONS.thinking: await api.setReasoning({ think: !s.model.think }); closePalette(); return;
      case ACTIONS.yolo: await api.setMode(s.permissionMode === 'yolo' ? 'normal' : 'yolo'); closePalette(); return;
      case ACTIONS.plan: await api.setInputMode(s.inputMode === 'plan' ? 'code' : 'plan'); closePalette(); return;
      case ACTIONS.cinematicToggle: setCinematic(!cinematic.value); closePalette(); return;
      case ACTIONS.motionToggle: setMotion(nextMotion(motion.value)); closePalette(); return;
    }
  };

  const run = async (item: PaletteItem, values?: Record<string, string>) => {
    if (running.current) return;
    running.current = true;
    setFailure(null);
    setFailed(null);
    const sid = s.sessionId;
    try {
      switch (item.run.kind) {
        case 'action':
          // An action that declares arguments opens the same form a command
          // does; runAction receives the filled values.
          if (!values && item.args?.length) { argumentItem.value = item.id; return; }
          await runAction(item.run.action, values);
          return;
        case 'command':
          if (!values && item.args?.length) { argumentItem.value = item.id; return; }
          if (!sid) throw new ApiError(400, 'missing_session', 'There is no session to run a command in');
          await api.runCommand(sid, item.run.id, values ?? {});
          closePalette();
          return;
        case 'mcp':
          if (!values && item.args?.length) { argumentItem.value = item.id; return; }
          if (!sid) throw new ApiError(400, 'missing_session', 'There is no session to run a prompt in');
          await api.runMcpPrompt(sid, item.run.clientId, item.run.promptId, values ?? {});
          closePalette();
          return;
        case 'session': await api.follow(item.run.id); closePalette(); return;
        case 'model': await api.selectModel('main', item.run.provider, item.run.model); closePalette(); return;
        case 'skill': {
          const { attachment } = await api.attachSkill(item.run.name);
          attachFiles([new File([attachment.content], attachment.name, { type: attachment.mime_type })]);
          closePalette();
          return;
        }
        case 'skill-toggle': {
          const name = item.run.name;
          const skill = loaded.skills.find((k) => k.name === name);
          await api.setSkillDisabled(name, !skill?.disabled);
          await reload(['skills']);
          return;
        }
      }
    } catch (err) {
      if (describeFailure(err).tone === 'starting') setFailed({ item, values });
      setFailure(err);
    } finally {
      running.current = false;
    }
  };

  const onKeyDown = (e: KeyboardEvent) => {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      if (visible.length === 0) return;
      const d = e.key === 'ArrowDown' ? 1 : -1;
      setActive((current + d + visible.length) % visible.length);
      return;
    }
    if (e.key === 'Enter') {
      e.preventDefault();
      const item = visible[current];
      if (item) void run(item);
    }
  };

  const groups: { group: string; items: { item: PaletteItem; index: number }[] }[] = [];
  visible.forEach((item, index) => {
    const last = groups[groups.length - 1];
    if (last && last.group === item.group) last.items.push({ item, index });
    else groups.push({ group: item.group, items: [{ item, index }] });
  });

  return (
    <div class="palette-layer" inert={inert}>
      <div
        class="palette glass enter"
        role="dialog"
        aria-modal="true"
        aria-label="Command palette"
        ref={dialog}
        tabIndex={-1}
      >
        {argument ? (
          <ArgumentForm
            key={argument.id}
            title={argument.title}
            args={argument.args ?? []}
            onSubmit={(values) => void run(argument, values)}
            onCancel={() => { argumentItem.value = null; }}
          />
        ) : (
          <Fragment>
            <input
              id="palette-input"
              class="palette__input"
              ref={input}
              role="combobox"
              aria-label="Command palette"
              aria-autocomplete="list"
              aria-expanded={visible.length > 0}
              aria-controls="palette-list"
              aria-activedescendant={current >= 0 ? `palette-option-${current}` : undefined}
              placeholder="Search commands, sessions and models"
              value={query}
              onInput={(e) => setPaletteQuery((e.currentTarget as HTMLInputElement).value)}
              onKeyDown={onKeyDown}
            />
            <ul id="palette-list" class="palette__list" role="listbox" aria-label="Palette results">
              {groups.map((g) => (
                <Fragment key={g.group}>
                  <li role="presentation" aria-hidden="true" class="palette__group hud-label">{g.group}</li>
                  {g.items.map(({ item, index }) => (
                    <li
                      id={`palette-option-${index}`}
                      key={item.id}
                      role="option"
                      aria-selected={index === current}
                      class="palette__option"
                      onClick={() => void run(item)}
                    >
                      <span class="palette__title">
                        <span class="visually-hidden">{item.group}. </span>
                        {item.title}
                      </span>
                      {item.hint && <span class="palette__hint">{item.hint}</span>}
                    </li>
                  ))}
                </Fragment>
              ))}
            </ul>
            {/* The empty answer is not an option, so it lives outside the
                listbox and is announced politely. It stays mounted so the
                text lands in a region that was already there. */}
            <p
              class={visible.length === 0 ? 'palette__empty' : 'palette__empty visually-hidden'}
              role="status"
            >{visible.length === 0 ? 'No matches' : null}</p>
          </Fragment>
        )}
        {shown && (
          <div class="palette__status" role={shown.tone === 'starting' ? 'status' : 'alert'} data-tone={shown.tone}>
            <span>{shown.text}</span>
            {canRetry && (
              <button type="button" class="link-button" onClick={() => void retry()}>Retry</button>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
