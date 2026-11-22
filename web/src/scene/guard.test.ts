import { describe, expect, it } from 'vitest';
import { FrameGuard, GUARD } from './guard';

// Feed samples one frame every spacingMs, each costing workMs of work.
function feed(g: FrameGuard, workMs: number, spacingMs: number, fromMs: number, untilMs: number) {
  const changes: number[] = [];
  for (let now = fromMs; now <= untilMs; now += spacingMs) {
    const r = g.sample(workMs, now);
    if (r !== null) changes.push(r);
  }
  return changes;
}

describe('FrameGuard', () => {
  it('keeps everything on while frames are cheap', () => {
    expect(feed(new FrameGuard(), 6, 16, 0, 20_000)).toEqual([]);
  });

  it('measures render work, not the gap between frames', () => {
    // An idle scene renders every 33 ms; a 5 ms frame is far inside budget.
    expect(feed(new FrameGuard(), 5, 33.3, 0, 30_000)).toEqual([]);
  });

  it('steps down once per cooldown while frames stay expensive', () => {
    const g = new FrameGuard();
    expect(feed(g, 40, 40, 0, GUARD.windowMs + 40)).toEqual([1]);
    // Still slow, but inside the 10 s cooldown: no further step.
    expect(feed(g, 40, 40, GUARD.windowMs + 80, GUARD.windowMs + 9_000)).toEqual([]);
    expect(feed(g, 40, 40, GUARD.windowMs + 9_040, 40_000)).toEqual([2, 3]);
    expect(g.level).toBe(3);
  });

  it('never steps below the last level', () => {
    const g = new FrameGuard(3);
    expect(feed(g, 40, 40, 0, 60_000)).toEqual([]);
  });

  it('starts from the level it is given, so a remount cannot step back up', () => {
    const g = new FrameGuard(2);
    expect(g.level).toBe(2);
    expect(feed(g, 40, 40, 0, GUARD.windowMs + 40)).toEqual([3]);
  });

  it('ignores gaps such as a hidden tab', () => {
    const g = new FrameGuard();
    feed(g, 6, 16, 0, 1000);
    // One frame that took two minutes of work is a stall, not a trend.
    expect(g.sample(120_000, 121_000)).toBeNull();
    expect(feed(g, 6, 16, 121_016, 130_000)).toEqual([]);
  });

  it('does not finish a stale window after a long pause between frames', () => {
    const g = new FrameGuard();
    // Two seconds of slow frames, then the tab hides for a minute.
    feed(g, 40, 40, 0, 2000);
    expect(g.sample(6, 62_000)).toBeNull();
    expect(feed(g, 6, 16, 62_016, 70_000)).toEqual([]);
  });

  it('waits a cooldown after a restore', () => {
    const g = new FrameGuard(2);
    g.reset(0, 50_000);
    expect(feed(g, 40, 40, 50_040, 59_000)).toEqual([]);
    expect(feed(g, 40, 40, 59_040, 64_000)).toEqual([1]);
  });
});
