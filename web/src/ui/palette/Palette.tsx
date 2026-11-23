import { Fragment } from 'preact';
import { useEffect, useRef, useState } from 'preact/hooks';
import type { CommandEntry, McpPrompt, ModelChoice, Session, SkillEntry } from '../../api/types';
import { ApiError, api } from '../../api/http';
import { describeFailure } from '../../state/failure';
import { adoptSession } from '../../state/reducer';
import { cinematic, setCinematic } from '../../state/cinematic';
import { motion, setMotion, type Motion } from '../../state/motion';
import { toggleDrawer } from '../../state/drawer';
import { dispatch, state } from '../../state/store';
import {
  argumentItem, closePalette, openPalette, paletteOpen, paletteQuery, setPaletteQuery, type PaletteSection,
} from '../../state/palette';
import { useDialogFocus } from '../interrupt/focus';
import { attachFiles } from '../deck/CommandDeck';
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

// loadSources loads every source in parallel. One failure leaves its group
// out and is reported without a page log.
async function loadSources(): Promise<{ loaded: Loaded; errors: unknown[] }> {
  const [commands, prompts, skills, sessions, models] = await Promise.allSettled([
    api.commands(), api.mcpPrompts(), api.skills(), api.sessions(), api.providers(),
  ]);
  const errors: unknown[] = [];
  const take = <T,>(r: PromiseSettledResult<T>, fallback: T): T => {
    if (r.status === 'fulfilled') return r.value;
    errors.push(r.reason);
    return fallback;
  };
  return {
    loaded: {
      commands: take(commands, { commands: [] }).commands,
      prompts: take(prompts, { prompts: [] }).prompts,
      skills: take(skills, { skills: [] }).skills,
      sessions: take(sessions, [] as Session[]).map(({ id, title }) => ({ id, title })),
      models: take(models, { choices: [] }).choices,
    },
    errors,
  };
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
  const [errors, setErrors] = useState<unknown[]>([]);
  const [failure, setFailure] = useState<unknown | null>(null);
  const [active, setActive] = useState(0);
  const dialog = useRef<HTMLDivElement>(null);
  const argumentId = argumentItem.value;
  const query = paletteQuery.value;

  // The focus trap also restores the opener when the dialog unmounts, and
  // re-focuses the first control when the argument form comes and goes.
  useDialogFocus(dialog, `${section}:${argumentId ?? ''}`);

  const reload = async () => {
    const next = await loadSources();
    setLoaded(next.loaded);
    setErrors(next.errors);
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
    motion: motion.value,
  };
  const scoped = section === 'skills' ? buildSkillToggles(loaded.skills) : buildItems(sources);
  const keep = groupIn(section);
  const inSection = keep === null ? scoped : scoped.filter((i) => keep.includes(i.group));
  const visible = filterItems(inSection, query);
  const argument = argumentId === null ? undefined : inSection.find((i) => i.id === argumentId);
  const current = visible.length === 0 ? -1 : Math.min(active, visible.length - 1);

  const runAction = async (action: string) => {
    const sid = s.sessionId;
    switch (action) {
      case ACTIONS.newSession: {
        const created = await api.createSession();
        dispatch((cur) => adoptSession(cur, created.id));
        closePalette();
        return;
      }
      case ACTIONS.sessions: openPalette('sessions'); return;
      case ACTIONS.model: openPalette('models'); return;
      case ACTIONS.compact:
        if (!sid) throw new ApiError(400, 'missing_session', 'There is no session to compact');
        await api.summarize(sid);
        closePalette();
        return;
      case ACTIONS.reloadSkills: await api.reloadSkills(); closePalette(); return;
      case ACTIONS.toggleSkills: openPalette('skills'); return;
      case ACTIONS.drawer: toggleDrawer(); closePalette(); return;
      case ACTIONS.cinematic: setCinematic(!cinematic.value); closePalette(); return;
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
    setFailure(null);
    const sid = s.sessionId;
    try {
      switch (item.run.kind) {
        case 'action':
          await runAction(item.run.action);
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
          await reload();
          return;
        }
      }
    } catch (err) {
      setFailure(err);
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

  const err = failure ?? errors[0];
  const shown = err === undefined || err === null ? null : describeFailure(err);

  return (
    <div class="palette-layer" inert={inert}>
      <div class="palette glass enter" role="dialog" aria-modal="true" aria-label="Command palette" ref={dialog}>
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
              role="combobox"
              aria-label="Command palette"
              aria-autocomplete="list"
              aria-expanded="true"
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
              {visible.length === 0 && <li role="presentation" class="palette__empty">No matches</li>}
            </ul>
          </Fragment>
        )}
        {shown && (
          <div class="palette__status" role={shown.tone === 'starting' ? 'status' : 'alert'} data-tone={shown.tone}>
            <span>{shown.text}</span>
            {errors.length > 0 && (
              <button type="button" class="link-button" onClick={() => void reload()}>Retry</button>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
