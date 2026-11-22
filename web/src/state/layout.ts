import { signal } from '@preact/signals';

// The stage box in viewport coordinates. Stage publishes it and the scene
// layer reads it, so the projector can sit in the empty center column.
export const stageRect = signal<DOMRectReadOnly | null>(null);
