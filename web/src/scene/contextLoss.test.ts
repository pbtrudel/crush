import { describe, expect, it, vi } from 'vitest';
import { watchContextLoss } from './contextLoss';

describe('watchContextLoss', () => {
  it('reports a lost context and keeps the browser from restoring it silently', () => {
    const canvas = document.createElement('canvas');
    const onFail = vi.fn();
    watchContextLoss(canvas, onFail);
    const lost = new Event('webglcontextlost', { cancelable: true });
    canvas.dispatchEvent(lost);
    expect(onFail).toHaveBeenCalledTimes(1);
    expect(lost.defaultPrevented).toBe(true);
  });

  it('ignores other events and stops after it is disposed', () => {
    const canvas = document.createElement('canvas');
    const onFail = vi.fn();
    const stop = watchContextLoss(canvas, onFail);
    canvas.dispatchEvent(new Event('webglcontextrestored'));
    expect(onFail).not.toHaveBeenCalled();
    stop();
    canvas.dispatchEvent(new Event('webglcontextlost', { cancelable: true }));
    expect(onFail).not.toHaveBeenCalled();
  });
});
