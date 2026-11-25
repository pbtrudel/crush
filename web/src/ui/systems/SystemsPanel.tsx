import { useEffect, useRef, useState } from 'preact/hooks';
import type { DiagnosticCounts, LspServer, McpServer } from '../../api/types';
import { api } from '../../api/http';
import { describeFailure } from '../../state/failure';
import { without } from '../../state/record';
import type { ServerState } from '../../state/reducer';
import { state } from '../../state/store';
import { systemsFocus, systemsOpen } from '../../state/palette';
import { serverTone, type ServerTone } from '../hud/series';
import { HexTile } from '../hud/ServerHexes';
import { useDialogFocus, usePaletteDeckFocus } from '../interrupt/focus';
import { webAddress } from '../webAddress';

type Kind = 'lsp' | 'mcp';
type McpAction = 'start' | 'refresh' | 'enable' | 'disable';

// The state words name the Go enums in plain English; the color alone never
// carries the state.
const MCP_WORDS: Record<string, string> = {
  connected: 'running',
  starting: 'starting',
  error: 'error',
  'needs auth': 'needs sign-in',
  disabled: 'disabled',
  lazy: 'starts on first use',
};

const LSP_WORDS: Record<string, string> = {
  '0': 'not started',
  '1': 'starting',
  '2': 'ready',
  '3': 'error',
  '4': 'stopped',
  '5': 'disabled',
};

const SIGN_IN_TERMINAL = 'Finish signing in from the terminal';
const NO_WEB_ADDRESS = 'The MCP server did not return a web address';

function plural(count: number, one: string, many: string): string {
  return `${count} ${count === 1 ? one : many}`;
}

// diagnosticText speaks the error and warning counts, and says so when a
// server has neither.
function diagnosticText(counts: DiagnosticCounts | undefined, live: number): string {
  if (counts === undefined) return live > 0 ? plural(live, 'diagnostic', 'diagnostics') : 'no problems';
  const parts: string[] = [];
  if (counts.error > 0) parts.push(plural(counts.error, 'error', 'errors'));
  if (counts.warning > 0) parts.push(plural(counts.warning, 'warning', 'warnings'));
  return parts.length === 0 ? 'no problems' : parts.join(', ');
}

// A row merges the fetched list with the live frame map: the list carries
// what frames do not (error text, diagnostic counts, the disabled field),
// the frames carry the current state.
interface Row<T> {
  name: string;
  item?: T;
  live?: ServerState;
}

function mergeRows<T extends { name: string }>(list: T[] | null, live: Record<string, ServerState>): Row<T>[] {
  const rows: Row<T>[] = (list ?? []).map((item) => ({ name: item.name, item, live: live[item.name] }));
  const extra = Object.keys(live).filter((name) => !rows.some((row) => row.name === name)).sort();
  for (const name of extra) rows.push({ name, live: live[name] });
  return rows;
}

function mcpWord(row: Row<McpServer>): { state: string; word: string; tone: ServerTone } {
  const state = row.live?.state ?? row.item?.state ?? '';
  const base = MCP_WORDS[state] ?? 'unknown';
  const word = state === 'error' && row.item?.error ? `${base}: ${row.item.error}` : base;
  return { state, word, tone: serverTone('mcp', state) };
}

function lspWord(row: Row<LspServer>): { state: string; word: string; tone: ServerTone } {
  const state = row.live?.state ?? row.item?.state ?? '';
  const base = LSP_WORDS[state] ?? 'unknown';
  const word = state === '3' && row.item?.error ? `${base}: ${row.item.error}` : base;
  return { state, word, tone: serverTone('lsp', state) };
}

export function SystemsPanel({ inert }: { inert: boolean }) {
  if (systemsOpen.value === null) return null;
  return <SystemsDialog inert={inert} />;
}

function SystemsDialog({ inert }: { inert: boolean }) {
  const s = state.value;
  const [lsp, setLsp] = useState<LspServer[] | null>(null);
  const [mcp, setMcp] = useState<McpServer[] | null>(null);
  const [lspError, setLspError] = useState<unknown | null>(null);
  const [mcpError, setMcpError] = useState<unknown | null>(null);
  const [liveCounts, setLiveCounts] = useState<Record<string, DiagnosticCounts>>({});
  const [busy, setBusy] = useState<string[]>([]);
  const [failures, setFailures] = useState<Record<string, unknown>>({});
  const [lspFailure, setLspFailure] = useState<unknown | null>(null);
  const [countFailure, setCountFailure] = useState<unknown | null>(null);
  const [notes, setNotes] = useState<Record<string, string>>({});
  const [warnings, setWarnings] = useState<Record<string, string>>({});
  const [links, setLinks] = useState<Record<string, string>>({});
  const dialog = useRef<HTMLDivElement>(null);
  const lspHeading = useRef<HTMLHeadingElement>(null);
  const mcpHeading = useRef<HTMLHeadingElement>(null);
  const alive = useRef(true);
  const lspGen = useRef(0);
  const mcpGen = useRef(0);
  const headingRequest = useRef<Kind | null>(null);
  // The button a user pressed, so a frame that turns it into another one can
  // move focus into its card instead of letting it fall to the body.
  const pressed = useRef<{ name: string; el: HTMLElement | null } | null>(null);
  // The live diagnostic count each row was drawn with; a change asks for
  // that server's counts alone.
  const seenDiagnostics = useRef<Record<string, number>>({});
  const countGen = useRef<Record<string, number>>({});
  const countInFlight = useRef<Set<string>>(new Set());
  const countDirty = useRef<Set<string>>(new Set());
  const busyRef = useRef<Set<string>>(new Set());

  useDialogFocus(dialog, 'systems', inert);
  usePaletteDeckFocus();

  // An answer can arrive after the panel closed; the flag keeps it from
  // reaching an unmounted tree, and each section's counter drops a slower
  // older answer.
  useEffect(() => {
    alive.current = true;
    return () => { alive.current = false; };
  }, []);

  // The opener decides the section: the HUD group's own, or the first one
  // the palette lands on.
  useEffect(() => {
    const section = systemsFocus.value === 'mcp' ? mcpHeading : lspHeading;
    section.current?.focus();
  }, []);

  const loadLsp = async (retry = false): Promise<boolean> => {
    const gen = ++lspGen.current;
    try {
      const { servers } = await api.lspServers();
      if (!alive.current || gen !== lspGen.current) return false;
      setLsp(servers);
      setLspError(null);
      if (retry) headingRequest.current = 'lsp';
      return true;
    } catch (err) {
      if (!alive.current || gen !== lspGen.current) return false;
      setLspError(err);
      return false;
    }
  };

  const loadMcp = async (retry = false): Promise<boolean> => {
    const gen = ++mcpGen.current;
    try {
      const { servers } = await api.mcpServers();
      if (!alive.current || gen !== mcpGen.current) return false;
      setMcp(servers);
      setMcpError(null);
      if (retry) headingRequest.current = 'mcp';
      return true;
    } catch (err) {
      if (!alive.current || gen !== mcpGen.current) return false;
      setMcpError(err);
      return false;
    }
  };

  useEffect(() => {
    void loadLsp();
    void loadMcp();
  }, []);

  // A successful retry hands focus to the section heading it replaced.
  useEffect(() => {
    const section = headingRequest.current;
    if (section === null) return;
    headingRequest.current = null;
    (section === 'lsp' ? lspHeading : mcpHeading).current?.focus();
  });

  const begin = (key: string): boolean => {
    if (busyRef.current.has(key)) return false;
    busyRef.current.add(key);
    setBusy((current) => [...current, key]);
    return true;
  };

  const settle = (key: string): void => {
    busyRef.current.delete(key);
    if (!alive.current) return;
    setBusy((current) => current.filter((k) => k !== key));
  };

  // A new action on a server replaces whatever its last one said.
  const clearServer = (name: string) => {
    setFailures((current) => without(current, name));
    setNotes((current) => without(current, name));
    setWarnings((current) => without(current, name));
    setLinks((current) => without(current, name));
  };

  const runMcpAction = async (name: string, action: McpAction, button: HTMLElement | null) => {
    const key = `${name}:${action}`;
    if (!begin(key)) return;
    pressed.current = { name, el: button };
    clearServer(name);
    try {
      await api.mcpAction(name, action);
    } catch (err) {
      if (!alive.current) return;
      setFailures((current) => ({ ...current, [name]: err }));
    } finally {
      settle(key);
    }
  };

  const runAuth = async (name: string, button: HTMLElement | null) => {
    const key = `${name}:auth`;
    if (!begin(key)) return;
    pressed.current = { name, el: button };
    clearServer(name);
    try {
      const { url } = await api.mcpAuth(name);
      if (!alive.current) return;
      const href = webAddress(url);
      if (href !== null) {
        // The answer can take seconds, so a popup blocker may refuse it and
        // noopener always answers null; the inline link is there either way.
        window.open(href, '_blank', 'noopener,noreferrer');
        setLinks((current) => ({ ...current, [name]: href }));
      } else if (url === '') {
        setNotes((current) => ({ ...current, [name]: SIGN_IN_TERMINAL }));
      } else {
        setWarnings((current) => ({ ...current, [name]: NO_WEB_ADDRESS }));
      }
    } catch (err) {
      if (!alive.current) return;
      setFailures((current) => ({ ...current, [name]: err }));
    } finally {
      settle(key);
    }
  };

  const runLspAll = async (action: 'start' | 'stop') => {
    const key = `lsp:${action}`;
    if (!begin(key)) return;
    setLspFailure(null);
    try {
      if (action === 'start') await api.lspStart();
      else await api.lspStop();
    } catch (err) {
      if (alive.current) setLspFailure(err);
    } finally {
      settle(key);
    }
  };

  // One counts request per server at a time; a change that lands while one
  // runs asks for one more answer once it settles.
  const refreshCounts = async (name: string, target: number) => {
    if (countInFlight.current.has(name)) {
      countDirty.current.add(name);
      return;
    }
    countInFlight.current.add(name);
    const gen = (countGen.current[name] ?? 0) + 1;
    countGen.current[name] = gen;
    try {
      const next = await api.lspDiagnostics(name);
      if (alive.current && countGen.current[name] === gen) {
        setLiveCounts((current) => ({ ...current, [name]: next }));
        setCountFailure(null);
      }
    } catch (err) {
      // The counts the list carried stay on screen, and the section's failure
      // line says the refresh did not land; the next change asks again.
      if (alive.current) setCountFailure(err);
    } finally {
      countInFlight.current.delete(name);
      const latest = state.value.lsp[name]?.diagnostics;
      if (alive.current && countDirty.current.delete(name) && latest !== undefined && latest !== target) {
        void refreshCounts(name, latest);
      }
    }
  };

  // A frame that changes one server's diagnostic count refreshes that
  // server's counts only; the list itself is never fetched again.
  useEffect(() => {
    const listed = new Set((lsp ?? []).map((server) => server.name));
    for (const [name, server] of Object.entries(s.lsp)) {
      const before = seenDiagnostics.current[name];
      if (before === undefined || before === server.diagnostics) {
        if (listed.has(name)) seenDiagnostics.current[name] = server.diagnostics;
        continue;
      }
      if (!listed.has(name)) continue;
      seenDiagnostics.current[name] = server.diagnostics;
      void refreshCounts(name, server.diagnostics);
    }
  }, [s.lsp, lsp]);

  // A button that a frame removed has handed focus to the body; the card's
  // first button takes it back.
  useEffect(() => {
    const press = pressed.current;
    if (press === null) return;
    if (press.el !== null && press.el.isConnected) {
      if (document.activeElement === press.el) return;
      pressed.current = null;
      return;
    }
    pressed.current = null;
    const active = document.activeElement;
    if (active !== null && active !== document.body && active !== document.documentElement) return;
    const card = Array.from(dialog.current?.querySelectorAll<HTMLElement>('.systems__card') ?? [])
      .find((c) => c.dataset.server === press.name);
    const first = card?.querySelector<HTMLElement>('button');
    (first ?? dialog.current)?.focus();
  });

  const lspRows = mergeRows(lsp, s.lsp);
  const mcpRows = mergeRows(mcp, s.mcp);
  const shownLspLoad = lspError === null ? null : describeFailure(lspError);
  const shownMcpLoad = mcpError === null ? null : describeFailure(mcpError);
  const shownLspFailure = lspFailure === null && countFailure === null
    ? null
    : describeFailure(lspFailure ?? countFailure);

  const lspButton = (label: string, action: 'start' | 'stop') => (
    <button
      type="button"
      class="systems__action"
      aria-disabled={busy.includes(`lsp:${action}`) ? 'true' : undefined}
      onClick={() => void runLspAll(action)}
    >{label}</button>
  );

  const mcpButton = (name: string, label: string, action: McpAction | 'auth') => (
    <button
      type="button"
      class="systems__action"
      aria-disabled={busy.includes(`${name}:${action}`) ? 'true' : undefined}
      onClick={(e) => {
        const button = e.currentTarget as HTMLElement;
        if (action === 'auth') void runAuth(name, button);
        else void runMcpAction(name, action, button);
      }}
    >{label}</button>
  );

  const lspCard = (row: Row<LspServer>) => {
    const { word, tone } = lspWord(row);
    const counts = liveCounts[row.name] ?? row.item?.diagnostics;
    return (
      <li key={row.name} class="systems__card" data-server={row.name} data-tone={tone}>
        <HexTile tone={tone} />
        <div class="systems__body">
          <span class="systems__name">{row.name}</span>
          <span class="systems__state" data-tone={tone}>{word}</span>
          <span class="systems__counts">{diagnosticText(counts, row.live?.diagnostics ?? 0)}</span>
        </div>
      </li>
    );
  };

  const mcpCard = (row: Row<McpServer>) => {
    const { state: liveState, word, tone } = mcpWord(row);
    const tools = row.live?.tools ?? row.item?.tools ?? 0;
    // A frame is fresher than the list, so when one exists it decides both
    // flags; a fetched false never masks a live change.
    const disabled = row.live === undefined
      ? row.item?.disabled === true
      : liveState === 'disabled';
    const needsAuth = row.live === undefined
      ? row.item?.needs_auth === true
      : liveState === 'needs auth';
    const failure = failures[row.name];
    const shown = failure === undefined ? null : describeFailure(failure);
    const link = links[row.name];
    const note = notes[row.name];
    const warning = warnings[row.name];
    return (
      <li key={row.name} class="systems__card" data-server={row.name} data-tone={tone}>
        <HexTile tone={tone} />
        <div class="systems__body">
          <span class="systems__name">{row.name}</span>
          <span class="systems__state" data-tone={tone}>{word}</span>
          {tools > 0 && <span class="systems__counts">{plural(tools, 'tool', 'tools')}</span>}
          <div class="systems__buttons">
            {liveState === 'connected' && mcpButton(row.name, 'Refresh', 'refresh')}
            {(liveState === 'error' || liveState === 'lazy') && mcpButton(row.name, 'Start', 'start')}
            {needsAuth && mcpButton(row.name, 'Authenticate', 'auth')}
            {disabled ? mcpButton(row.name, 'Enable', 'enable') : mcpButton(row.name, 'Disable', 'disable')}
          </div>
          {link !== undefined && (
            <p class="systems__note">
              <a href={link} target="_blank" rel="noopener noreferrer">Open the sign-in page</a>
            </p>
          )}
          {note !== undefined && <p class="systems__note">{note}</p>}
          {warning !== undefined && (
            <div class="systems__status" role="alert" data-tone="error">{warning}</div>
          )}
          {shown && (
            <div
              class="systems__status"
              role={shown.tone === 'starting' ? 'status' : 'alert'}
              data-tone={shown.tone}
            >{shown.text}</div>
          )}
        </div>
      </li>
    );
  };

  return (
    <div class="systems-layer" inert={inert}>
      <div
        class="systems glass scanlines enter"
        role="dialog"
        aria-modal="true"
        aria-label="Systems"
        ref={dialog}
        tabIndex={-1}
      >
        <section class="systems__section" aria-labelledby="systems-lsp-heading">
          <div class="systems__head">
            <h2 id="systems-lsp-heading" class="systems__heading" ref={lspHeading} tabIndex={-1}>Language servers</h2>
            <div class="systems__buttons">
              {lspButton('Start language servers', 'start')}
              {lspButton('Stop language servers', 'stop')}
            </div>
          </div>
          <ul class="systems__list">{lspRows.map(lspCard)}</ul>
          {shownLspFailure && (
            <div
              class="systems__status"
              role={shownLspFailure.tone === 'starting' ? 'status' : 'alert'}
              data-tone={shownLspFailure.tone}
            >{shownLspFailure.text}</div>
          )}
          {shownLspLoad && (
            <div
              class="systems__status"
              role={shownLspLoad.tone === 'starting' ? 'status' : 'alert'}
              data-tone={shownLspLoad.tone}
            >
              <span>{shownLspLoad.text}</span>
              <button type="button" class="link-button" onClick={() => void loadLsp(true)}>Retry</button>
            </div>
          )}
        </section>
        <section class="systems__section" aria-labelledby="systems-mcp-heading">
          <div class="systems__head">
            <h2 id="systems-mcp-heading" class="systems__heading" ref={mcpHeading} tabIndex={-1}>MCP servers</h2>
          </div>
          <ul class="systems__list">{mcpRows.map(mcpCard)}</ul>
          {shownMcpLoad && (
            <div
              class="systems__status"
              role={shownMcpLoad.tone === 'starting' ? 'status' : 'alert'}
              data-tone={shownMcpLoad.tone}
            >
              <span>{shownMcpLoad.text}</span>
              <button type="button" class="link-button" onClick={() => void loadMcp(true)}>Retry</button>
            </div>
          )}
        </section>
      </div>
    </div>
  );
}
