import { useEffect, useRef } from 'preact/hooks';
import { state } from '../state/store';
import { needsAttention, needsYou } from '../state/reducer';
import { drawerOpen, effectivePanelTab, panelTab } from '../state/drawer';
import { handOffLogFocus, logInert, watchFocusHandoff, watchOverlayWidth } from '../state/overlay';
import { motion, lowPower } from '../state/motion';
import { cinematic, cinematicAllowed, cinematicKey, setCinematic } from '../state/cinematic';
import { BootSequence } from './BootSequence';
import { CineHint } from './CineHint';
import { ConnectionBanner } from './ConnectionBanner';
import { CommandDeck } from './deck/CommandDeck';
import { Hud } from './Hud';
import { SideAnswer } from './log/SideAnswer';
import { TransmissionLog } from './log/TransmissionLog';
import { Stage } from './stage/Stage';
import { SceneLayer } from './stage/SceneLayer';
import { Toasts } from './Toasts';
import { Unauthorized } from './Unauthorized';
import { faviconFor, projectHue, tabTitle } from './tabIdentity';
import { Interrupt } from './interrupt/Interrupt';
import { Inspector } from './inspector/Inspector';
import { closeInspector, escapeLeavesDraft } from './inspector/escape';
import { PanelTabs } from './PanelTabs';
import { FileDrawer } from './files/FileDrawer';
import {
  closePanels, deckMode, escapeModels, escapePalette, escapeSessions, escapeSystems, modelsOpen,
  openPalette, paletteOpen, sessionsOpen, systemsOpen,
} from '../state/palette';
import { Palette } from './palette/Palette';
import { SessionSwitcher } from './sessions/SessionSwitcher';
import { closeSignIn, escapeSignIn, signInOpen } from '../state/signin';
import { ModelSelector } from './models/ModelSelector';
import { SystemsPanel } from './systems/SystemsPanel';
import { SignInDialog } from './signin/SignInDialog';
import { AwsSsoCard } from './signin/AwsSsoCard';
import { usePublishedHeight } from './usePublishedHeight';

export function App() {
  const s = state.value;
  const attention = needsAttention(s);
  const you = needsYou(s);
  const view = useRef<HTMLDivElement>(null);
  const camera = useRef<HTMLDivElement>(null);
  const log = useRef<HTMLElement>(null);
  const inspector = useRef<HTMLElement>(null);
  const deck = useRef<HTMLDivElement>(null);
  const logCovered = useRef(false);

  // signin.css places the AWS SSO card above the whole deck column, the side
  // answer included, so the column publishes its own height.
  usePublishedHeight(deck, '--deck-h');

  // The log goes inert while the overlay drawer covers it: that is a width
  // plus drawer state, not something a stylesheet can express.
  useEffect(() => watchOverlayWidth(), []);

  // A log that leaves the tab order under the focus would keep it on an
  // inert control, so the drawer takes the focus and hands it back to the
  // handle when it closes over it.
  useEffect(() => {
    handOffLogFocus(logInert.value, logCovered.current);
    logCovered.current = logInert.value;
  }, [logInert.value]);
  useEffect(() => watchFocusHandoff(), []);

  useEffect(() => {
    document.title = tabTitle(s.project || 'crush', s.session?.title ?? '', you);
  }, [s.project, s.session?.title, you]);

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
      const target = e.target;
      // The palette opens from anywhere, the prompt included, and stays
      // closed while a dialog owns the page. It closes every other layer
      // first, so two dialogs never stack.
      if (e.code === 'KeyK' && (e.ctrlKey || e.metaKey) && !e.altKey && !e.repeat) {
        if (needsAttention(state.value)) return;
        e.preventDefault();
        const paletteWasOpen = paletteOpen.value !== null;
        closePanels();
        if (signInOpen.value) closeSignIn();
        if (!paletteWasOpen) openPalette();
        return;
      }
      // A permission or a question owns Escape while it is open: the dialogs
      // under the interrupt layer are inert, and the question form skips its
      // batch on the same press.
      if (e.key === 'Escape' && needsAttention(state.value)) return;
      // The palette is the innermost layer: it takes Escape before the
      // sessions switcher, the Display menu, the inspector and the cinematic
      // view. Its argument form hands the list back on the first press.
      if (e.key === 'Escape' && paletteOpen.value !== null) {
        e.preventDefault();
        escapePalette();
        return;
      }
      // The switcher is next: its open rename or delete confirm is cancelled
      // before the dialog closes.
      if (e.key === 'Escape' && sessionsOpen.value) {
        e.preventDefault();
        escapeSessions();
        return;
      }
      // The model selector has no inner layer, so its first Escape closes it
      // and its focus trap hands focus back to the HUD model name.
      if (e.key === 'Escape' && modelsOpen.value) {
        e.preventDefault();
        escapeModels();
        return;
      }
      // The systems panel has no inner layer either; its trap returns focus
      // to the HUD group that opened it.
      if (e.key === 'Escape' && systemsOpen.value !== null) {
        e.preventDefault();
        escapeSystems();
        return;
      }
      // The sign-in dialog has no inner layer either; its trap returns focus
      // to the control that opened it.
      if (e.key === 'Escape' && signInOpen.value) {
        e.preventDefault();
        escapeSignIn();
        return;
      }
      // An open Display menu owns Escape: it closes itself, so the inspector
      // and the cinematic view must not read the same press.
      if (e.key === 'Escape' && target instanceof Element && target.closest('details.motion-menu[open]')) return;
      // Escape in the deck's note mode leaves that mode and does nothing
      // else, so this capture listener must not read the same press for the
      // inspector or the cinematic view.
      if (e.key === 'Escape' && focused instanceof HTMLElement && focused.id === 'deck-input' && deckMode.value === 'note') return;
      const action = cinematicKey({
        code: e.code,
        altKey: e.altKey,
        inPrompt: focused instanceof HTMLElement && focused.id === 'deck-input',
        dialogOpen: needsAttention(state.value),
        active: cinematic.value,
        allowed: cinematicAllowed(),
      });
      if (!action) return;
      e.preventDefault();
      // Escape closes the innermost thing first: an open inspector, then the
      // cinematic view. A draft in the message box outranks both.
      if (action === 'exit' && state.value.inspected) {
        if (!escapeLeavesDraft(e)) closeInspector();
        return;
      }
      setCinematic(action === 'toggle' ? !cinematic.value : false);
    };
    window.addEventListener('keydown', onKeyDown, true);
    return () => window.removeEventListener('keydown', onKeyDown, true);
  }, []);

  if (s.conn === 'unauthorized') return <Unauthorized />;
  const modal = attention && s.conn === 'live';
  // Below 1024px the agent tab is the inspector's slot; with no inspector
  // the log shows there, whatever tab was last picked.
  const tab = effectivePanelTab(panelTab.value, s.inspected !== null);
  return (
    <div
      class="holodeck"
      data-motion={motion.value}
      data-low-power={lowPower.value}
      data-conn={s.conn}
      data-cinematic={cinematic.value ? 'true' : 'false'}
    >
      <SceneLayer view={view} camera={camera} panels={[log, inspector]} />
      <BootSequence done={s.conn !== 'booting'} />
      <Hud />
      <ConnectionBanner />
      <PanelTabs inert={modal} />
      <main class="holodeck__grid" inert={modal} data-drawer={drawerOpen.value ? 'open' : 'closed'} data-panel-tab={tab}>
        <div class="cine-view" ref={view}>
          <div class="cine-camera" ref={camera}>
            <section class="panel panel--log glass scanlines" aria-label="Transmission log" ref={log} inert={modal || logInert.value}>
              <TransmissionLog />
            </section>
            <Inspector panelRef={inspector} />
          </div>
        </div>
        <section class="stage" aria-label="Agent constellation">
          <Stage />
        </section>
        <FileDrawer />
      </main>
      <div class="holodeck__deck" inert={modal} ref={deck}>
        <SideAnswer />
        <CommandDeck />
      </div>
      <Interrupt />
      <Palette inert={modal} />
      <SessionSwitcher inert={modal} />
      <ModelSelector inert={modal} />
      <SystemsPanel inert={modal} />
      <SignInDialog inert={modal} />
      <AwsSsoCard inert={modal} />
      <Toasts />
      <CineHint />
    </div>
  );
}
