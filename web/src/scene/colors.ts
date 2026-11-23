import * as THREE from 'three';
import { token } from './palette';

export interface SceneColors {
  void: THREE.Color;
  haze: THREE.Color;
  cyan: THREE.Color;
  magenta: THREE.Color;
  violet: THREE.Color;
  teal: THREE.Color;
  amber: THREE.Color;
}

// jsdom and a missing token fall back to the given sRGB hex, which is the
// value of the token named beside each entry.
const FALLBACKS = {
  void: ['--void', '#030318'],
  haze: ['--haze', '#3d006f'],
  cyan: ['--cyan', '#00efff'],
  magenta: ['--magenta', '#ff24d5'],
  violet: ['--violet', '#9754ff'],
  teal: ['--teal', '#15e5b4'],
  amber: ['--amber', '#ffb51c'],
} as const;

// readColors resolves the scene palette once per mount, so the WebGL scene
// follows tokens.css without reading the stylesheet every frame.
export function readColors(): SceneColors {
  return {
    void: token(...FALLBACKS.void),
    haze: token(...FALLBACKS.haze),
    cyan: token(...FALLBACKS.cyan),
    violet: token(...FALLBACKS.violet),
    magenta: token(...FALLBACKS.magenta),
    teal: token(...FALLBACKS.teal),
    amber: token(...FALLBACKS.amber),
  };
}
