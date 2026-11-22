import type { ServerState } from '../../state/reducer';
import { serverTone, type ServerTone } from './series';

const GLYPH = '8,1 14,4.5 14,13.5 8,17 2,13.5 2,4.5';

// The visible tone names, since "lit" and "dim" mean nothing when spoken.
const TONE_WORDS: Record<ServerTone, string> = { lit: 'ready', dim: 'stopped', error: 'error' };

// hexLabel names a server and, when it has any, its diagnostic or tool
// count, so the spoken name carries the numbers the tiles no longer show.
function hexLabel(kind: 'lsp' | 'mcp', name: string, server: ServerState, tone: ServerTone): string {
  const parts = [`${name}: ${TONE_WORDS[tone]}`];
  if (kind === 'lsp' && server.diagnostics > 0) parts.push(`${server.diagnostics} diag`);
  if (kind === 'mcp' && server.tools > 0) parts.push(`${server.tools} tools`);
  return parts.join(', ');
}

// ServerHexes is one small hexagon per language server or MCP server, with
// a dim placeholder when none are configured.
export function ServerHexes({ kind, servers }: { kind: 'lsp' | 'mcp'; servers: Record<string, ServerState> }) {
  const entries = Object.entries(servers);
  if (entries.length === 0) {
    return (
      <span class="hexes" data-kind={kind}>
        <span class="hexes__none">none</span>
      </span>
    );
  }
  return (
    <span class="hexes" data-kind={kind}>
      {entries.map(([name, server]) => {
        const tone = serverTone(kind, server.state);
        const label = hexLabel(kind, name, server, tone);
        return (
          <svg key={name} class="hex" data-tone={tone} viewBox="0 0 16 18" width={11} height={12} role="img" aria-label={label}>
            <title>{label}</title>
            <polygon points={GLYPH} />
            {tone === 'error' && <text class="hex__bang" x={8} y={12} text-anchor="middle">!</text>}
          </svg>
        );
      })}
    </span>
  );
}
