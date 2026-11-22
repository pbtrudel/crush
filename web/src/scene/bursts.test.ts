import { describe, expect, it, vi } from 'vitest';
import * as THREE from 'three';
import { BURST_MS, categoryColor, createBursts, MAX_BURSTS } from './bursts';
import { readColors } from './colors';

const colors = readColors();

describe('categoryColor', () => {
  it('maps read, write, run and network to cyan, teal, amber and violet', () => {
    expect(categoryColor(colors, 'read').equals(colors.cyan)).toBe(true);
    expect(categoryColor(colors, 'write').equals(colors.teal)).toBe(true);
    expect(categoryColor(colors, 'run').equals(colors.amber)).toBe(true);
    expect(categoryColor(colors, 'network').equals(colors.violet)).toBe(true);
  });
});

describe('createBursts', () => {
  it('expands a ring and removes it after the burst time', () => {
    const b = createBursts(colors, new THREE.Vector3(0, 1.6, 0));
    b.spawn('write');
    expect(b.active()).toBe(1);
    const ring = b.group.children.find((o) => o.visible) as THREE.Mesh;
    expect((ring.material as THREE.MeshBasicMaterial).color.equals(colors.teal)).toBe(true);
    const start = ring.scale.x;
    expect(b.update(0.3)).toBe(true);
    expect(ring.scale.x).toBeGreaterThan(start);
    b.update(BURST_MS / 1000);
    expect(b.active()).toBe(0);
    expect(b.update(0.016)).toBe(false);
  });

  it('disposes the shared geometry and the materials, and empties the group', () => {
    const b = createBursts(colors, new THREE.Vector3());
    b.spawn('read');
    const ring = b.group.children[0] as THREE.Mesh;
    const geometry = vi.spyOn(ring.geometry, 'dispose');
    const material = vi.spyOn(ring.material as THREE.Material, 'dispose');

    b.dispose();
    expect(geometry).toHaveBeenCalled();
    expect(material).toHaveBeenCalled();
    expect(b.group.children).toHaveLength(0);
  });

  it('recycles the oldest ring past the cap', () => {
    const b = createBursts(colors, new THREE.Vector3());
    for (let i = 0; i < MAX_BURSTS + 5; i++) b.spawn('read');
    expect(b.active()).toBe(MAX_BURSTS);
    expect(b.group.children).toHaveLength(MAX_BURSTS);
  });
});
