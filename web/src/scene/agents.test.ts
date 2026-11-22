import { describe, expect, it } from 'vitest';
import * as THREE from 'three';
import { createAgentNodes, type SceneAgent } from './agents';
import { readColors } from './colors';
import { ORBIT } from './orbit';

const CORE = new THREE.Vector3(0, ORBIT.height, 0);
const teammate: SceneAgent = { id: 'team-m-1', kind: 'teammate', status: 'running' };
const subagent: SceneAgent = { id: 'sub-1', kind: 'subagent', status: 'running' };

function nodes() {
  const colors = readColors();
  return { colors, n: createAgentNodes(colors, CORE) };
}

describe('agent nodes', () => {
  it('builds one prism and one filament per agent, colored by kind', () => {
    const { colors, n } = nodes();
    n.sync([teammate, subagent], null);
    expect(n.ids().sort()).toEqual(['sub-1', 'team-m-1']);
    const bodies = n.group.children.filter((c) => c.name === 'agent-node') as THREE.Mesh[];
    expect(bodies).toHaveLength(2);
    expect(n.group.children.filter((c) => c.name === 'agent-filament')).toHaveLength(2);
    const color = (id: string) => ((bodies.find((b) => b.userData.id === id)!.material) as THREE.MeshBasicMaterial).color;
    expect(color('team-m-1').getHex()).toBe(colors.cyan.getHex());
    expect(color('sub-1').getHex()).toBe(colors.violet.getHex());
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
    expect((body.material as THREE.MeshBasicMaterial).color.getHex()).toBe(colors.magenta.getHex());
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

  it('disposes its geometries and materials and empties the group', () => {
    const { n } = nodes();
    n.sync([teammate, subagent], null);
    const disposed: string[] = [];
    n.group.traverse((o) => {
      const m = o as THREE.Mesh;
      if (m.geometry) m.geometry.addEventListener('dispose', () => disposed.push('g'));
      if (m.material) (m.material as THREE.Material).addEventListener('dispose', () => disposed.push('m'));
    });
    n.dispose();
    expect(disposed.filter((d) => d === 'm').length).toBeGreaterThanOrEqual(6);
    expect(disposed).toContain('g');
    expect(n.group.children).toHaveLength(0);
  });
});
