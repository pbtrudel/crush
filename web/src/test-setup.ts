// jsdom logs a "not implemented" notice on every HTMLCanvasElement
// getContext call, and the WebGL probe plus the particle texture both call
// it. Tests that need a context install their own stub over this one.
HTMLCanvasElement.prototype.getContext = (() => null) as unknown as typeof HTMLCanvasElement.prototype.getContext;
