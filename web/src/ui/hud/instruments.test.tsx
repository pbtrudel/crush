import { describe, expect, it } from 'vitest';
import { render } from 'preact';
import { ContextGauge } from './ContextGauge';
import { Numeral } from './Numeral';

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
