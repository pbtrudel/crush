import * as THREE from 'three';
import { stepColor } from './core';
import { token } from './palette';

export interface SceneState {
  busy: boolean;
  contextFill: number;
  attention: boolean;
  motion: 'full' | 'reduced' | 'off';
}

interface Palette { cyan: THREE.Color; violet: THREE.Color; magenta: THREE.Color; amber: THREE.Color }

function contextArc(fill: number, p: Palette): THREE.Mesh {
  const len = Math.max(0.0001, Math.PI * 2 * fill);
  const geo = new THREE.RingGeometry(1.42, 1.5, 128, 1, Math.PI / 2, len);
  const mat = new THREE.MeshBasicMaterial({
    color: fill > 0.8 ? p.amber : p.cyan, transparent: true, opacity: 0.9,
    side: THREE.DoubleSide, blending: THREE.AdditiveBlending, depthWrite: false,
  });
  const mesh = new THREE.Mesh(geo, mat);
  mesh.rotation.x = -Math.PI / 2;
  mesh.position.y = 0.01;
  return mesh;
}

// mountConstellation draws the holographic projector: a wireframe core
// hovering over concentric light rings, a context-window arc on the
// platform, and a hex lattice fading into fog. It only renders while
// something moves, and stops when the tab is hidden.
export function mountConstellation(canvas: HTMLCanvasElement, initial: SceneState) {
  let state = initial;
  const palette: Palette = {
    cyan: token('--cyan', '#00efff'),
    violet: token('--violet', '#9754ff'),
    magenta: token('--magenta', '#ff24d5'),
    amber: token('--amber', '#ffb51c'),
  };
  const { cyan: CYAN, violet: VIOLET, magenta: MAGENTA } = palette;
  const renderer = new THREE.WebGLRenderer({ canvas, antialias: true, alpha: true });
  renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
  const scene = new THREE.Scene();
  scene.fog = new THREE.FogExp2(0x060a1a, 0.09);
  const camera = new THREE.PerspectiveCamera(42, 1, 0.1, 100);
  camera.position.set(0, 2.4, 7.2);
  camera.lookAt(0, 0.8, 0);

  const coreMat = new THREE.MeshBasicMaterial({ color: CYAN.clone(), wireframe: true, transparent: true, opacity: 0.85 });
  const core = new THREE.Mesh(new THREE.IcosahedronGeometry(0.7, 1), coreMat);
  core.position.y = 1.15;
  const glowMat = new THREE.MeshBasicMaterial({ color: CYAN.clone(), transparent: true, opacity: 0.22, blending: THREE.AdditiveBlending, depthWrite: false });
  const glow = new THREE.Mesh(new THREE.SphereGeometry(0.42, 32, 32), glowMat);
  core.add(glow);
  scene.add(core);

  const rings = new THREE.Group();
  [1.1, 1.75, 2.45].forEach((r, i) => {
    const m = new THREE.Mesh(
      new THREE.RingGeometry(r, r + 0.025 + i * 0.01, 160),
      new THREE.MeshBasicMaterial({ color: i === 1 ? VIOLET : CYAN, transparent: true, opacity: 0.6 - i * 0.15, side: THREE.DoubleSide, blending: THREE.AdditiveBlending, depthWrite: false }),
    );
    m.rotation.x = -Math.PI / 2;
    rings.add(m);
  });
  scene.add(rings);

  const beam = new THREE.Mesh(
    new THREE.CylinderGeometry(0.45, 1.15, 1.1, 48, 1, true),
    new THREE.MeshBasicMaterial({ color: CYAN, transparent: true, opacity: 0.07, side: THREE.DoubleSide, blending: THREE.AdditiveBlending, depthWrite: false }),
  );
  beam.position.y = 0.55;
  scene.add(beam);

  const lattice = new THREE.Group();
  const hexEdges = new THREE.EdgesGeometry(new THREE.CircleGeometry(0.14, 6));
  for (let i = 0; i < 42; i++) {
    const line = new THREE.LineSegments(hexEdges, new THREE.LineBasicMaterial({ color: i % 5 === 0 ? VIOLET : CYAN, transparent: true, opacity: 0.28 }));
    const theta = (i / 42) * Math.PI * 2;
    const r = 3.1 + (i % 3) * 0.45;
    line.position.set(Math.cos(theta) * r, 0.4 + ((i * 37) % 23) / 10, Math.sin(theta) * r);
    line.lookAt(0, line.position.y, 0);
    lattice.add(line);
  }
  scene.add(lattice);

  let arc = contextArc(state.contextFill, palette);
  scene.add(arc);

  // A resize or frame queued before dispose must not touch a released
  // renderer, so every entry point checks this flag.
  let disposed = false;

  const resize = () => {
    if (disposed) return;
    const { clientWidth: w, clientHeight: h } = canvas;
    if (!w || !h) return;
    renderer.setSize(w, h, false);
    camera.aspect = w / h;
    camera.updateProjectionMatrix();
    kick();
  };
  const ro = new ResizeObserver(resize);
  ro.observe(canvas);

  let raf = 0;
  let last = performance.now();
  // Reduced motion renders single frames on each update, so state changes
  // (attention color, context arc) still show without continuous motion.
  const animating = () => !disposed && !document.hidden && state.motion === 'full';

  function draw(now: number) {
    if (disposed) return;
    const dt = Math.min(0.05, (now - last) / 1000);
    last = now;
    if (state.motion !== 'off') {
      const speed = state.busy ? 0.9 : 0.22;
      core.rotation.y += dt * speed;
      core.rotation.x += dt * speed * 0.3;
      rings.rotation.y -= dt * 0.08;
      lattice.rotation.y += dt * 0.02;
      const pulse = state.busy ? 1 + Math.sin(now / 180) * 0.08 : 1;
      glow.scale.setScalar(pulse);
    }
    const target = state.attention ? MAGENTA : CYAN;
    stepColor(coreMat.color, target, state.motion);
    stepColor(glowMat.color, target, state.motion);
    renderer.render(scene, camera);
    raf = animating() ? requestAnimationFrame(draw) : 0;
  }

  function kick() {
    if (disposed) return;
    if (!raf) raf = requestAnimationFrame((t) => { last = t; draw(t); });
  }

  const onVisibility = () => kick();
  document.addEventListener('visibilitychange', onVisibility);
  resize();

  return {
    update(next: SceneState) {
      if (next.contextFill !== state.contextFill) {
        scene.remove(arc);
        arc.geometry.dispose();
        (arc.material as THREE.Material).dispose();
        arc = contextArc(next.contextFill, palette);
        scene.add(arc);
      }
      state = next;
      kick();
    },
    dispose() {
      disposed = true;
      cancelAnimationFrame(raf);
      raf = 0;
      ro.disconnect();
      document.removeEventListener('visibilitychange', onVisibility);
      // The scene graph holds every geometry and material; release them so a
      // low power toggle does not leak GPU memory across remounts.
      const geometries = new Set<THREE.BufferGeometry>();
      const materials = new Set<THREE.Material>();
      scene.traverse((obj) => {
        const mesh = obj as THREE.Mesh;
        if (!mesh.geometry) return;
        geometries.add(mesh.geometry);
        const mat = mesh.material as THREE.Material | THREE.Material[] | undefined;
        if (Array.isArray(mat)) mat.forEach((m) => materials.add(m));
        else if (mat) materials.add(mat);
      });
      geometries.forEach((g) => g.dispose());
      materials.forEach((m) => m.dispose());
      renderer.dispose();
    },
  };
}
