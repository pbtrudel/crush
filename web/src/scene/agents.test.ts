import { describe, expect, it } from 'vitest';
import * as THREE from 'three';
import { BODY_INTENSITY, createAgentNodes, type SceneAgent } from './agents';
import { readColors } from './colors';
import { ORBIT } from './orbit';

const CORE = new THREE.Vector3(0, ORBIT.height, 0);
const teammate: SceneAgent = { id: 'team-m-1', kind: 'teammate', status: 'running' };
const subagent: SceneAgent = { id: 'sub-1', kind: 'subagent', status: 'running' };

function nodes() {
  const colors = readColors();
  return { colors, n: createAgentNodes(colors, CORE) };
}

const tinted = (c: THREE.Color) => c.clone().multiplyScalar(BODY_INTENSITY);

describe('agent nodes', () => {
  it('builds one prism and one filament per agent, colored by kind', () => {
    const { colors, n } = nodes();
    n.sync([teammate, subagent], null);
    expect(n.ids().sort()).toEqual(['sub-1', 'team-m-1']);
    const bodies = n.group.children.filter((c) => c.name === 'agent-node') as THREE.Mesh[];
    expect(bodies).toHaveLength(2);
    expect(n.group.children.filter((c) => c.name === 'agent-filament')).toHaveLength(2);
    const color = (id: string) => ((bodies.find((b) => b.userData.id === id)!.material) as THREE.MeshBasicMaterial).color;
    expect(color('team-m-1').getHex()).toBe(tinted(colors.cyan).getHex());
    expect(color('sub-1').getHex()).toBe(tinted(colors.violet).getHex());
  });

  it('dims an inactive node and shows only the wireframe while provisioning', () => {
    const { n } = nodes();
    n.sync([{ ...teammate, status: 'inactive' }, { ...subagent, status: 'provisioning' }], null);
    n.update(0.016, 0, 'full');
    const body = (id: string) => n.group.children.find((c) => c.name === 'agent-node' && c.userData.id === id) as THREE.Mesh;
    expect((body('team-m-1').material as THREE.MeshBasicMaterial).opacity).toBeLessThan(0.5);
    expect(body('sub-1').visible).toBe(false);
    expect(n.group.children.find((c) => c.name === 'agent-wire' && c.userData.id === 'sub-1')!.visible).toBe(true);
  });

  it('turns a failed node magenta', () => {
    const { colors, n } = nodes();
    n.sync([{ ...teammate, status: 'failed' }], null);
    n.update(0.016, 0, 'full');
    const body = n.group.children.find((c) => c.name === 'agent-node') as THREE.Mesh;
    expect((body.material as THREE.MeshBasicMaterial).color.getHex()).toBe(tinted(colors.magenta).getHex());
  });

  it('paints the prism over the frame so the projector halo cannot wash it out', () => {
    const { colors, n } = nodes();
    n.sync([teammate, subagent], null);
    n.update(0.016, 0, 'off');
    const body = (id: string) => (n.group.children.find((c) => c.name === 'agent-node' && c.userData.id === id) as THREE.Mesh);
    const material = (id: string) => body(id).material as THREE.MeshBasicMaterial;
    // Additive cyan summed into the core's white pedestal, which left the
    // near teammate node an invisible pale blob at 1440x860. The body is
    // composited over the frame instead, at an intensity that keeps it a
    // saturated prism beside the halo.
    expect(material('team-m-1').blending).toBe(THREE.NormalBlending);
    expect(material('team-m-1').color.getHex()).toBe(tinted(colors.cyan).getHex());
    expect(material('sub-1').color.getHex()).toBe(tinted(colors.violet).getHex());
    expect(BODY_INTENSITY).toBeLessThan(1);
  });

  it('collapses a node that leaves the roster into the core, then removes it', () => {
    const { n } = nodes();
    n.sync([teammate], null);
    n.update(0.016, 0, 'full');
    n.sync([], null);
    expect(n.update(0.3, 0.3, 'full')).toBe(true);
    const p = new THREE.Vector3();
    expect(n.position('team-m-1', p)).toBe(false);
    for (let i = 0; i < 10; i++) n.update(0.1, 1 + i * 0.1, 'full');
    expect(n.ids()).toEqual([]);
    expect(n.group.children.filter((c) => c.name === 'agent-node')).toHaveLength(0);
  });

  it('holds still with motion off and drifts in full motion', () => {
    const { n } = nodes();
    n.sync([teammate], null);
    const a = new THREE.Vector3();
    const b = new THREE.Vector3();
    n.update(0.016, 0, 'off');
    n.position('team-m-1', a);
    n.update(0.016, 30, 'off');
    n.position('team-m-1', b);
    expect(b.distanceTo(a)).toBe(0);
    n.update(0.016, 30, 'full');
    n.position('team-m-1', b);
    expect(b.distanceTo(a)).toBeGreaterThan(0.1);
  });

  it('reports a pulse as animation until it lands', () => {
    const { colors, n } = nodes();
    n.sync([teammate], null);
    n.update(0.016, 0, 'off');
    n.pulse('team-m-1', colors.teal);
    expect(n.update(0.1, 0.1, 'off')).toBe(true);
    for (let i = 0; i < 20; i++) n.update(0.1, 0.2 + i * 0.1, 'off');
    expect(n.update(0.1, 3, 'off')).toBe(false);
  });

  it('enlarges the selected node', () => {
    const { n } = nodes();
    n.sync([teammate, subagent], 'sub-1');
    n.update(0.016, 0, 'off');
    const scale = (id: string) => n.group.children.find((c) => c.name === 'agent-node' && c.userData.id === id)!.scale.x;
    expect(scale('sub-1')).toBeGreaterThan(scale('team-m-1'));
  });

  it('keeps a filament out of frustum culling while its far end moves', () => {
    const { n } = nodes();
    n.sync([teammate], null);
    const filament = n.group.children.find((c) => c.name === 'agent-filament') as THREE.Line;
    // The bounding sphere comes from the two points at the core; the moving
    // end is an attribute update, so three would cull a filament whose node
    // is on screen whenever the core is not.
    expect(filament.frustumCulled).toBe(false);
  });

  it('disposes the prism, the edges, every filament and the spark', () => {
    const { n } = nodes();
    n.sync([teammate, subagent], null);
    const named = (name: string) => n.group.children.filter((c) => c.name === name);
    const bodies = named('agent-node') as THREE.Mesh[];
    const wires = named('agent-wire') as THREE.LineSegments[];
    const filaments = named('agent-filament') as THREE.Line[];
    const sparks = named('agent-spark') as THREE.Mesh[];
    // The prism, the edge list and the spark are shared; a filament is not.
    const shared = [bodies[0].geometry, wires[0].geometry, sparks[0].geometry];
    expect(new Set(shared).size).toBe(3);
    expect(bodies[1].geometry).toBe(bodies[0].geometry);
    expect(wires[1].geometry).toBe(wires[0].geometry);
    expect(sparks[1].geometry).toBe(sparks[0].geometry);
    expect(filaments[0].geometry).not.toBe(filaments[1].geometry);

    const gone = new Set<THREE.BufferGeometry>();
    const materials = new Set<THREE.Material>();
    const materialsGone = new Set<THREE.Material>();
    const expected = [...shared, ...filaments.map((f) => f.geometry)];
    for (const geometry of expected) geometry.addEventListener('dispose', () => gone.add(geometry));
    for (const object of [...bodies, ...wires, ...filaments, ...sparks]) {
      const material = object.material as THREE.Material;
      materials.add(material);
      material.addEventListener('dispose', () => materialsGone.add(material));
    }

    n.dispose();
    for (const geometry of expected) expect(gone.has(geometry)).toBe(true);
    // Four materials per node: the body, the wire, the filament and the spark.
    expect(materials.size).toBe(8);
    for (const material of materials) expect(materialsGone.has(material)).toBe(true);
    expect(n.group.children).toHaveLength(0);
  });
});
