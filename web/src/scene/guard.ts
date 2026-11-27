import type { EffectsLevel } from '../state/effects';

export const GUARD = { budgetMs: 25, windowMs: 3000, cooldownMs: 10_000, maxGapMs: 250 };

// FrameGuard watches how long the render work takes and steps the effects
// down when the average stays over budget. It measures one frame's work, not
// the interval between frames, so an idle 30 fps scene never trips it. A
// single stalled frame or a pause such as a hidden tab restarts the window
// instead of counting toward it.
export class FrameGuard {
  private levelValue: EffectsLevel;
  private sum = 0;
  private count = 0;
  private windowStart = -Infinity;
  private lastAt = -Infinity;
  private lastChange = -Infinity;

  constructor(level: EffectsLevel = 0) {
    this.levelValue = level;
  }

  get level(): EffectsLevel {
    return this.levelValue;
  }

  sample(workMs: number, now: number): EffectsLevel | null {
    if (workMs > GUARD.maxGapMs || now - this.lastAt > GUARD.maxGapMs) {
      this.restart(now);
      return null;
    }
    this.lastAt = now;
    this.sum += workMs;
    this.count++;
    if (now - this.windowStart < GUARD.windowMs) return null;

    const avg = this.sum / this.count;
    this.restart(now);
    if (avg <= GUARD.budgetMs || this.levelValue >= 3) return null;
    if (now - this.lastChange < GUARD.cooldownMs) return null;
    this.levelValue = (this.levelValue + 1) as EffectsLevel;
    this.lastChange = now;
    return this.levelValue;
  }

  reset(level: EffectsLevel, now: number): void {
    this.levelValue = level;
    this.lastChange = now;
    this.lastAt = now;
    this.restart(now);
  }

  private restart(now: number): void {
    this.sum = 0;
    this.count = 0;
    this.windowStart = now;
    this.lastAt = now;
  }
}
