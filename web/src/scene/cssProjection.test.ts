import { describe, expect, it } from 'vitest';
import * as THREE from 'three';
import { CSS3DObject, CSS3DRenderer } from 'three/addons/renderers/CSS3DRenderer.js';
import { cameraCSS, objectCSS } from './cssProjection';

// jsdom stores style.transform through CSSStyleDeclaration, which parses the
// string and writes it back with spaces between the functions and six
// significant digits. Compare the function sequence and the numbers, both
// read from the same parser, so the reference stays three's own renderer.
function parse(css: string): { name: string; args: number[] }[] {
  return (css.match(/[a-z0-9]+\([^)]*\)/g) ?? []).map((fn) => {
    const name = fn.slice(0, fn.indexOf('('));
    const args = fn.slice(fn.indexOf('(') + 1, -1).match(/-?[\d.]+(?:e[-+]?\d+)?/g) ?? [];
    return { name, args: args.map(Number) };
  });
}

const round = (n: number) => Number(n.toPrecision(5));

function expectSameTransform(got: string, want: string) {
  const a = parse(got);
  const b = parse(want);
  expect(a.map((f) => f.name)).toEqual(b.map((f) => f.name));
  expect(a.map((f) => f.args.map(round))).toEqual(b.map((f) => f.args.map(round)));
}

describe('css projection', () => {
  it('matches CSS3DRenderer for the camera and an object', () => {
    const camera = new THREE.PerspectiveCamera(42, 1440 / 860, 0.1, 100);
    camera.position.set(0, 4.6, 11.5);
    camera.lookAt(0, 1.2, 0);
    camera.updateMatrixWorld();
    const scene = new THREE.Scene();
    const el = document.createElement('div');
    const obj = new CSS3DObject(el);
    obj.position.set(-3, 1.5, 0);
    obj.rotation.y = 0.6;
    obj.scale.setScalar(0.004);
    scene.add(obj);
    const r = new CSS3DRenderer();
    r.setSize(1440, 860);
    r.render(scene, camera);
    const viewEl = r.domElement.firstElementChild as HTMLElement;
    const cameraEl = viewEl.firstElementChild as HTMLElement;
    expect(cameraEl.style.transform).toContain('perspective(');
    expectSameTransform(cameraCSS(camera, 1440, 860), cameraEl.style.transform);
    expectSameTransform(objectCSS(obj), el.style.transform);
    expect(objectCSS(obj)).toMatch(/^translate\(-50%,-50%\)matrix3d\(/);
  });
});
