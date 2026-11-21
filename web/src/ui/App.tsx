import { useEffect } from 'preact/hooks';
import { state } from '../state/store';
import { needsAttention } from '../state/reducer';
import { motion, lowPower } from '../state/motion';
import { BootSequence } from './BootSequence';
import { ConnectionBanner } from './ConnectionBanner';
import { Hud } from './Hud';
import { Toasts } from './Toasts';
import { Unauthorized } from './Unauthorized';
import { faviconFor, projectHue, tabTitle } from './tabIdentity';
// Later tasks fill the layout slots below:
// import { TransmissionLog } from './log/TransmissionLog'; // Task 13
// import { Stage } from './stage/Stage'; // Task 12
// import { CommandDeck } from './deck/CommandDeck'; // Task 14
// import { Interrupt } from './interrupt/Interrupt'; // Task 15

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
      <BootSequence done={s.conn !== 'booting'} />
      <Hud />
      <ConnectionBanner />
      <main class="holodeck__grid" inert={modal}>
        {/* Task 13: <TransmissionLog /> */}
        <section class="panel panel--log" aria-label="Transmission log" />
        {/* Task 12: <Stage /> */}
        <section class="stage" aria-label="Agent constellation" />
      </main>
      {/* Task 14: <CommandDeck /> */}
      <div class="holodeck__deck" inert={modal} />
      {/* Task 15: <Interrupt /> */}
      <Toasts />
    </div>
  );
}
