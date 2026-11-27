import type { ServerState } from '../../state/reducer';
import { cinematic } from '../../state/cinematic';
import { openSystems } from '../../state/palette';
import { serverTone, type ServerTone } from './series';

const GLYPH = '8,1 14,4.5 14,13.5 8,17 2,13.5 2,4.5';

// The visible tone names, since "lit" and "dim" mean nothing when spoken.
const TONE_WORDS: Record<ServerTone, string> = { lit: 'ready', dim: 'stopped', error: 'error' };

// hexLabel names a server and, when it has any, its diagnostic or tool
// count, so the tile's tooltip still carries the numbers.
function hexLabel(kind: 'lsp' | 'mcp', name: string, server: ServerState, tone: ServerTone): string {
  const parts = [`${name}: ${TONE_WORDS[tone]}`];
  if (kind === 'lsp' && server.diagnostics > 0) parts.push(`${server.diagnostics} diag`);
  if (kind === 'mcp' && server.tools > 0) parts.push(`${server.tools} tools`);
  return parts.join(', ');
}

// countText speaks one count for the group button's name.
function countText(kind: 'lsp' | 'mcp', count: number): string {
  if (count <= 0) return '';
  if (kind === 'lsp') return `${count} ${count === 1 ? 'diagnostic' : 'diagnostics'}`;
  return `${count} ${count === 1 ? 'tool' : 'tools'}`;
}

// groupLabel is the whole group in words: each server with its state and
// count, because the tiles are too small to name.
function groupLabel(kind: 'lsp' | 'mcp', servers: Record<string, ServerState>): string {
  const label = kind === 'lsp' ? 'LSP' : 'MCP';
  const entries = Object.entries(servers);
  if (entries.length === 0) return `${label} servers: none. Open systems`;
  const parts = entries.map(([name, server]) => {
    const tone = serverTone(kind, server.state);
    const count = countText(kind, kind === 'lsp' ? server.diagnostics : server.tools);
    return [name + ' ' + TONE_WORDS[tone], count].filter(Boolean).join(', ');
  });
  return `${label} servers: ${parts.join(', ')}. Open systems`;
}

// HexTile is the shared hexagon: one small tile in its tone, with the bang
// on an error. It is decorative, since the text beside it says the same
// thing; the optional label becomes the mouse tooltip.
export function HexTile({ tone, label }: { tone: ServerTone; label?: string }) {
  return (
    <svg class="hex" data-tone={tone} viewBox="0 0 16 18" width={11} height={12} aria-hidden="true">
      {label !== undefined && <title>{label}</title>}
      <polygon points={GLYPH} />
      {tone === 'error' && <text class="hex__bang" x={8} y={12} text-anchor="middle">!</text>}
    </svg>
  );
}

// A real config can carry nine MCP servers. The HUD has room for a few
// tiles, so past this many the group shows one tile in its worst tone and
// the count, which keeps the strip inside the window at 1280.
const MAX_TILES = 3;

const TONE_RANK: Record<ServerTone, number> = { lit: 0, dim: 1, error: 2 };

// groupTone sums a collapsed group: an error outranks a stopped server, and
// a stopped one outranks a ready group.
function groupTone(kind: 'lsp' | 'mcp', servers: Record<string, ServerState>): ServerTone {
  let worst: ServerTone = 'lit';
  for (const server of Object.values(servers)) {
    const tone = serverTone(kind, server.state);
    if (TONE_RANK[tone] > TONE_RANK[worst]) worst = tone;
  }
  return worst;
}

// ServerHexes is one small hexagon per language server or MCP server, and
// the whole group is one button that opens the systems panel.
export function ServerHexes({ kind, servers }: { kind: 'lsp' | 'mcp'; servers: Record<string, ServerState> }) {
  const entries = Object.entries(servers);
  return (
    <button
      type="button"
      class="hexes hud__servers-btn"
      data-kind={kind}
      // Cinematic mode hides the strip, so an invisible control leaves the
      // tab order with it.
      tabIndex={cinematic.value ? -1 : 0}
      aria-label={groupLabel(kind, servers)}
      onClick={() => openSystems(kind)}
    >
      {entries.length === 0
        ? <span class="hexes__none">none</span>
        : entries.length > MAX_TILES
          ? (
            <>
              <HexTile tone={groupTone(kind, servers)} label={`${entries.length} servers`} />
              <span class="hexes__count" aria-hidden="true">{entries.length}</span>
            </>
          )
          : entries.map(([name, server]) => {
            const tone = serverTone(kind, server.state);
            return <HexTile key={name} tone={tone} label={hexLabel(kind, name, server, tone)} />;
          })}
    </button>
  );
}
