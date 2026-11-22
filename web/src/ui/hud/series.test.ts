import { describe, expect, it } from 'vitest';
import { activityBars, digits, serverTone, sparkPoints } from './series';

describe('sparkPoints', () => {
  it('draws a flat midline for fewer than two values', () => {
    expect(sparkPoints([], 60, 16)).toBe('0,8 60,8');
    expect(sparkPoints([5], 60, 16)).toBe('0,8 60,8');
  });

  it('scales values into the box with the top at the maximum', () => {
    expect(sparkPoints([0, 5, 10], 60, 16)).toBe('0,16 30,8 60,0');
  });

  it('draws a flat line when all values are equal', () => {
    expect(sparkPoints([3, 3, 3], 60, 16)).toBe('0,8 30,8 60,8');
  });
});

describe('activityBars', () => {
  const at = (minAgo: number) => 15 * 60_000 - minAgo * 60_000 - 1;
  it('buckets events per minute by category, oldest first', () => {
    const bars = activityBars([
      { id: 'a', category: 'read', at: at(0) },
      { id: 'b', category: 'read', at: at(0) },
      { id: 'c', category: 'write', at: at(2) },
      { id: 'd', category: 'run', at: at(14) },
      { id: 'e', category: 'network', at: at(15) },
    ], 15 * 60_000);
    expect(bars).toHaveLength(15);
    expect(bars[14]).toEqual({ read: 2, write: 0, run: 0, network: 0 });
    expect(bars[12]).toEqual({ read: 0, write: 1, run: 0, network: 0 });
    expect(bars[0]).toEqual({ read: 0, write: 0, run: 1, network: 0 });
  });
});

describe('serverTone', () => {
  it('lights ready LSPs and connected MCPs and flags errors', () => {
    expect(serverTone('lsp', '2')).toBe('lit');
    expect(serverTone('lsp', '3')).toBe('error');
    expect(serverTone('lsp', '1')).toBe('dim');
    expect(serverTone('mcp', 'connected')).toBe('lit');
    expect(serverTone('mcp', 'error')).toBe('error');
    expect(serverTone('mcp', 'needs auth')).toBe('error');
    expect(serverTone('mcp', 'disabled')).toBe('dim');
  });
});

describe('digits', () => {
  it('splits a readout into characters', () => {
    expect(digits('$0.12')).toEqual(['$', '0', '.', '1', '2']);
  });
});
