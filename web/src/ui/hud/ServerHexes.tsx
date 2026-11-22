import type { ServerState } from '../../state/reducer';
import { serverTone } from './series';

const GLYPH = '8,1 14,4.5 14,13.5 8,17 2,13.5 2,4.5';

// ServerHexes is one small lit hexagon per language server or MCP server.
// The title carries the server name and its tone for screen readers.
export function ServerHexes({ kind, servers }: { kind: 'lsp' | 'mcp'; servers: Record<string, ServerState> }) {
  return (
    <span class="hexes" data-kind={kind}>
      {Object.entries(servers).map(([name, server]) => {
        const tone = serverTone(kind, server.state);
        return (
          <svg key={name} class="hex" data-tone={tone} viewBox="0 0 16 18" width={11} height={12} role="img">
            <title>{name}: {tone}</title>
            <polygon points={GLYPH} />
            {tone === 'error' && <text class="hex__bang" x={8} y={12} text-anchor="middle">!</text>}
          </svg>
        );
      })}
    </span>
  );
}
