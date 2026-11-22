import { useEffect, useRef } from 'preact/hooks';
import { state } from '../state/store';
import { needsAttention } from '../state/reducer';
import { motion, lowPower } from '../state/motion';
import { cinematic, cinematicAllowed, cinematicKey, setCinematic } from '../state/cinematic';
import { BootSequence } from './BootSequence';
import { ConnectionBanner } from './ConnectionBanner';
import { CommandDeck } from './deck/CommandDeck';
import { Hud } from './Hud';
import { TransmissionLog } from './log/TransmissionLog';
import { Stage } from './stage/Stage';
import { SceneLayer } from './stage/SceneLayer';
import { Toasts } from './Toasts';
import { Unauthorized } from './Unauthorized';
import { faviconFor, projectHue, tabTitle } from './tabIdentity';
import { Interrupt } from './interrupt/Interrupt';

export function App() {
  const s = state.value;
  const attention = needsAttention(s);
  const view = useRef<HTMLDivElement>(null);
  const camera = useRef<HTMLDivElement>(null);
  const log = useRef<HTMLElement>(null);

  useEffect(() => {
    document.title = tabTitle(s.project || 'crush', s.session?.title ?? '', attention);
  }, [s.project, s.session?.title, attention]);

  useEffect(() => {
    if (!s.workingDir) return;
    const link = document.querySelector<HTMLLinkElement>('link[rel="icon"]');
    if (link) link.href = faviconFor(projectHue(s.workingDir));
  }, [s.workingDir]);

  // One capture-phase listener for the page: Alt+C must be stopped before it
  // types a character into the prompt, and Esc only leaves from outside it.
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      const focused = document.activeElement;
      const action = cinematicKey({
        code: e.code,
        altKey: e.altKey,
        key: e.key,
        inPrompt: focused instanceof HTMLElement && focused.id === 'deck-input',
        dialogOpen: needsAttention(state.value),
        active: cinematic.value,
        allowed: cinematicAllowed(),
      });
      if (!action) return;
      e.preventDefault();
      setCinematic(action === 'toggle' ? !cinematic.value : false);
    };
    window.addEventListener('keydown', onKeyDown, true);
    return () => window.removeEventListener('keydown', onKeyDown, true);
  }, []);

  if (s.conn === 'unauthorized') return <Unauthorized />;
  const modal = attention && s.conn === 'live';
  return (
    <div
      class="holodeck"
      data-motion={motion.value}
      data-low-power={lowPower.value}
      data-conn={s.conn}
      data-cinematic={cinematic.value ? 'true' : 'false'}
    >
      <SceneLayer view={view} camera={camera} panels={[log]} />
      <BootSequence done={s.conn !== 'booting'} />
      <Hud />
      <ConnectionBanner />
      <main class="holodeck__grid" inert={modal}>
        <div class="cine-view" ref={view}>
          <div class="cine-camera" ref={camera}>
            <section class="panel panel--log glass scanlines" aria-label="Transmission log" ref={log}>
              <TransmissionLog />
            </section>
          </div>
        </div>
        <section class="stage" aria-label="Agent constellation">
          <Stage />
        </section>
      </main>
      <div class="holodeck__deck" inert={modal}>
        <CommandDeck />
      </div>
      <Interrupt />
      <Toasts />
    </div>
  );
}
