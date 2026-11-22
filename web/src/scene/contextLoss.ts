// A lost context cannot be rebuilt in place; the caller retries by mounting a
// fresh renderer. Calling preventDefault keeps the browser from restoring
// the old context behind our back.
export function watchContextLoss(canvas: HTMLCanvasElement, onFail: () => void): () => void {
  const onLost = (e: Event) => {
    e.preventDefault();
    onFail();
  };
  canvas.addEventListener('webglcontextlost', onLost);
  return () => canvas.removeEventListener('webglcontextlost', onLost);
}
