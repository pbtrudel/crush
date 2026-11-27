// The agent orbit, shared by the three.js nodes and the 2D fallback. No
// three import, so the eager 2D view can use it.
export const ORBIT = { radius: 2.7, tilt: 0.32, height: 1.6, speed: 0.08 };

export function orbitAngle(index: number, count: number, elapsed: number, speed: number): number {
  return -Math.PI / 2 + (index * 2 * Math.PI) / Math.max(1, count) + elapsed * speed;
}

// orbitPoint puts an angle on a ring tilted toward the camera around the
// core, so nodes in front sit lower than nodes behind.
export function orbitPoint(angle: number): { x: number; y: number; z: number } {
  const x = Math.cos(angle) * ORBIT.radius;
  const flat = Math.sin(angle) * ORBIT.radius;
  return { x, y: ORBIT.height - flat * Math.sin(ORBIT.tilt), z: flat * Math.cos(ORBIT.tilt) };
}

// orbit2D places a node on the outer ring of the 2D constellation
// (ellipse rx 150, ry 34 at cy 70 in the SVG viewBox).
export function orbit2D(index: number, count: number): { x: number; y: number } {
  const a = orbitAngle(index, count, 0, 0) + Math.PI;
  return { x: 150 * Math.cos(a), y: 70 + 34 * Math.sin(a) };
}
