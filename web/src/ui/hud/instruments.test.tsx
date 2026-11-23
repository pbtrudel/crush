import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { render } from 'preact';
import { closeSystems, systemsOpen } from '../../state/palette';
import { setCinematic } from '../../state/cinematic';
import { resetWebglProbe } from '../../state/effects';
import { ContextGauge } from './ContextGauge';
import { Numeral } from './Numeral';
import { HexTile, ServerHexes } from './ServerHexes';
import { Sparkline } from './Sparkline';

// The cinematic rule needs a live 3D scene, and the probe reads the canvas
// context, so the stub has to be in place before the rule first evaluates.
const originalGetContext = HTMLCanvasElement.prototype.getContext;

beforeEach(() => {
  HTMLCanvasElement.prototype.getContext = (() => ({ getExtension: () => null })) as unknown as typeof originalGetContext;
  resetWebglProbe();
});

afterEach(() => {
  closeSystems();
  setCinematic(false);
  HTMLCanvasElement.prototype.getContext = originalGetContext;
  resetWebglProbe();
});

const mount = (vnode: preact.VNode) => {
  const root = document.createElement('div');
  render(vnode, root);
  return root;
};

describe('ContextGauge', () => {
  it('speaks its value and carries the level', () => {
    const root = mount(<ContextGauge fill={0.42} />);
    const meter = root.querySelector('[role="meter"]')!;
    expect(meter.getAttribute('aria-label')).toBe('Context');
    expect(meter.getAttribute('aria-valuenow')).toBe('42');
    expect(meter.getAttribute('aria-valuetext')).toBe('42 percent');
    expect(meter.getAttribute('data-level')).toBe('ok');
  });

  it('clamps the gauge', () => {
    for (const [fill, want, level] of [[Number.NaN, '0', 'ok'], [-1, '0', 'ok'], [3, '100', 'critical']] as const) {
      const meter = mount(<ContextGauge fill={fill} />).querySelector('[role="meter"]')!;
      expect(meter.getAttribute('aria-valuenow')).toBe(want);
      expect(meter.getAttribute('data-level')).toBe(level);
    }
  });
});

describe('Numeral', () => {
  it('rolls each digit with a data attribute and keeps the text readable', () => {
    const root = mount(<Numeral text="1.5k" label="Tokens" />);
    expect(root.querySelector('.visually-hidden')!.textContent).toBe('Tokens 1.5k');
    expect(Array.from(root.querySelectorAll('.roll')).map((e) => e.getAttribute('data-d'))).toEqual(['1', '5']);
    expect(root.innerHTML).not.toContain('style=');
  });
});

describe('Sparkline', () => {
  it('stays decorative because the numeral speaks the value', () => {
    const root = mount(<Sparkline values={[0, 5, 10]} />);
    const spark = root.querySelector('.spark')!;
    expect(spark.getAttribute('aria-hidden')).toBe('true');
    expect(spark.getAttribute('role')).toBeNull();
    expect(spark.getAttribute('aria-label')).toBeNull();
    expect(spark.querySelector('polyline')!.getAttribute('points')).toBe('0,16 30,8 60,0');
  });
});

describe('ServerHexes', () => {
  it('is one button per group that opens the systems panel', () => {
    const root = mount(
      <ServerHexes
        kind="lsp"
        servers={{
          gopls: { state: '2', diagnostics: 0, tools: 0 },
          tsserver: { state: '3', diagnostics: 4, tools: 0 },
        }}
      />,
    );
    const button = root.querySelector<HTMLButtonElement>('button.hexes')!;
    expect(button.getAttribute('aria-label'))
      .toBe('LSP servers: gopls ready, tsserver error, 4 diagnostics. Open systems');
    expect(button.tabIndex).toBe(0);

    // The tiles themselves stay decorative and keep their mouse tooltip.
    for (const hex of Array.from(root.querySelectorAll('.hex'))) {
      expect(hex.getAttribute('aria-hidden')).toBe('true');
      expect(hex.getAttribute('role')).toBeNull();
      expect(hex.getAttribute('aria-label')).toBeNull();
    }
    expect(Array.from(root.querySelectorAll('.hex title')).map((t) => t.textContent))
      .toEqual(['gopls: ready', 'tsserver: error, 4 diag']);

    button.click();
    expect(systemsOpen.value).toBe('lsp');
  });

  it('names an MCP group with its tools', () => {
    const root = mount(
      <ServerHexes
        kind="mcp"
        servers={{
          fs: { state: 'connected', diagnostics: 0, tools: 3 },
          gh: { state: 'needs auth', diagnostics: 0, tools: 0 },
        }}
      />,
    );
    expect(root.querySelector('button.hexes')!.getAttribute('aria-label'))
      .toBe('MCP servers: fs ready, 3 tools, gh error. Open systems');
  });

  it('names an empty group none', () => {
    const root = mount(<ServerHexes kind="mcp" servers={{}} />);
    const button = root.querySelector<HTMLButtonElement>('button.hexes')!;
    expect(button.getAttribute('aria-label')).toBe('MCP servers: none. Open systems');
    expect(button.querySelectorAll('.hex')).toHaveLength(0);
    expect(button.querySelector('.hexes__none')!.textContent).toBe('none');
    button.click();
    expect(systemsOpen.value).toBe('mcp');
  });

  it('leaves the tab order with the HUD in cinematic mode', () => {
    setCinematic(true);
    const root = mount(<ServerHexes kind="lsp" servers={{ gopls: { state: '2', diagnostics: 0, tools: 0 } }} />);
    expect(root.querySelector<HTMLButtonElement>('button.hexes')!.tabIndex).toBe(-1);
  });
});

describe('HexTile', () => {
  it('marks only an error tile with the bang glyph', () => {
    const error = mount(<HexTile tone="error" />).querySelector('.hex')!;
    expect(error.getAttribute('data-tone')).toBe('error');
    expect(error.getAttribute('aria-hidden')).toBe('true');
    expect(error.querySelector('.hex__bang')!.textContent).toBe('!');

    const ready = mount(<HexTile tone="lit" />).querySelector('.hex')!;
    expect(ready.querySelector('.hex__bang')).toBeNull();
    expect(ready.querySelector('title')).toBeNull();
  });
});
