import { useEffect } from 'preact/hooks';
import { state } from '../state/store';
import { needsAttention } from '../state/reducer';
import { motion, lowPower } from '../state/motion';
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

  useEffect(() => {
    document.title = tabTitle(s.project || 'crush', s.session?.title ?? '', attention);
  }, [s.project, s.session?.title, attention]);

  useEffect(() => {
    if (!s.workingDir) return;
    const link = document.querySelector<HTMLLinkElement>('link[rel="icon"]');
    if (link) link.href = faviconFor(projectHue(s.workingDir));
  }, [s.workingDir]);

  if (s.conn === 'unauthorized') return <Unauthorized />;
  const modal = attention && s.conn === 'live';
  return (
    <div class="holodeck" data-motion={motion.value} data-low-power={lowPower.value} data-conn={s.conn}>
      <SceneLayer />
      <BootSequence done={s.conn !== 'booting'} />
      <Hud />
      <ConnectionBanner />
      <main class="holodeck__grid" inert={modal}>
        <section class="panel panel--log glass scanlines" aria-label="Transmission log">
          <TransmissionLog />
        </section>
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
