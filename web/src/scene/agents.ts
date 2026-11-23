import * as THREE from 'three';
import type { AgentStatus } from '../state/agents';
import type { Motion } from '../state/motion';
import type { SceneColors } from './colors';
import { motionScale } from './motionScale';
import { ORBIT, orbitAngle, orbitPoint } from './orbit';

export interface SceneAgent {
  id: string;
  kind: 'teammate' | 'subagent';
  status: AgentStatus;
}

export interface AgentNodes {
  group: THREE.Group;
  sync(agents: SceneAgent[], selected: string | null): void;
  update(dt: number, elapsed: number, motion: Motion): boolean;
  position(id: string, out: THREE.Vector3): boolean;
  pulse(id: string, color: THREE.Color): void;
  flash(id: string, color: THREE.Color): void;
  ids(): string[];
  dispose(): void;
}

const RADIUS = 0.2;
const HEIGHT = 0.3;
const SPIN = 0.6;
const COLLAPSE_S = 0.6;
const PULSE_S = 0.7;
const FLASH_S = 0.5;
const FLICKER_S = 0.09;
const SELECTED_SCALE = 1.3;
const DIM = 0.35;
// The body paints its own colour instead of adding to the frame: additive
// cyan summed into the projector's white halo and the near node vanished.
// Scaling it keeps the prism a saturated cyan beside that halo.
export const BODY_INTENSITY = 0.75;

interface Node {
  agent: SceneAgent;
  index: number;
  body: THREE.Mesh;
  material: THREE.MeshBasicMaterial;
  wire: THREE.LineSegments;
  wireMaterial: THREE.LineBasicMaterial;
  filament: THREE.Line;
  filamentMaterial: THREE.LineBasicMaterial;
  spark: THREE.Mesh;
  sparkMaterial: THREE.MeshBasicMaterial;
  pulseAge: number;
  flashAge: number;
  flashColor: THREE.Color;
  // Seconds into the collapse once the node left the roster or finished;
  // negative while it is on the orbit.
  collapse: number;
  selected: boolean;
  // listed is false once the roster no longer carries the agent; such a
  // node is removed when its collapse ends.
  listed: boolean;
}

export function createAgentNodes(colors: SceneColors, core: THREE.Vector3): AgentNodes {
  const group = new THREE.Group();
  group.name = 'agents';
  const prism = new THREE.CylinderGeometry(RADIUS, RADIUS, HEIGHT, 6);
  const edges = new THREE.EdgesGeometry(prism);
  const sparkGeometry = new THREE.SphereGeometry(0.05, 8, 8);
  const nodes = new Map<string, Node>();
  let count = 0;

  const kindColor = (a: SceneAgent) => (a.kind === 'teammate' ? colors.cyan : colors.violet);
  const bodyColor = (c: THREE.Color) => c.clone().multiplyScalar(BODY_INTENSITY);

  const build = (agent: SceneAgent): Node => {
    const material = new THREE.MeshBasicMaterial({ color: bodyColor(kindColor(agent)), transparent: true, opacity: 1, depthWrite: false });
    const body = new THREE.Mesh(prism, material);
    body.name = 'agent-node';
    body.userData.id = agent.id;
    const wireMaterial = new THREE.LineBasicMaterial({ color: kindColor(agent).clone(), transparent: true, opacity: 0.9 });
    const wire = new THREE.LineSegments(edges, wireMaterial);
    wire.name = 'agent-wire';
    wire.userData.id = agent.id;
    const filamentGeometry = new THREE.BufferGeometry().setFromPoints([core.clone(), core.clone()]);
    const filamentMaterial = new THREE.LineBasicMaterial({ color: kindColor(agent).clone(), transparent: true, opacity: 0.35, blending: THREE.AdditiveBlending, depthWrite: false });
    const filament = new THREE.Line(filamentGeometry, filamentMaterial);
    filament.name = 'agent-filament';
    filament.userData.id = agent.id;
    // The bounding sphere is computed once from the two points at the core,
    // and the moving end is an attribute update, so three would cull the
    // filament whenever the core leaves the view.
    filament.frustumCulled = false;
    const sparkMaterial = new THREE.MeshBasicMaterial({ color: colors.cyan.clone(), transparent: true, opacity: 0, blending: THREE.AdditiveBlending, depthWrite: false });
    const spark = new THREE.Mesh(sparkGeometry, sparkMaterial);
    spark.name = 'agent-spark';
    spark.visible = false;
    group.add(body, wire, filament, spark);
    return {
      agent, index: 0, body, material, wire, wireMaterial, filament, filamentMaterial, spark, sparkMaterial,
      pulseAge: Infinity, flashAge: Infinity, flashColor: new THREE.Color(), collapse: -1, selected: false, listed: true,
    };
  };

  const remove = (id: string, n: Node) => {
    group.remove(n.body, n.wire, n.filament, n.spark);
    n.material.dispose();
    n.wireMaterial.dispose();
    n.filamentMaterial.dispose();
    n.filament.geometry.dispose();
    n.sparkMaterial.dispose();
    nodes.delete(id);
  };

  const sync = (agents: SceneAgent[], selected: string | null) => {
    const present = new Set(agents.map((a) => a.id));
    agents.forEach((agent, index) => {
      const n = nodes.get(agent.id) ?? build(agent);
      nodes.set(agent.id, n);
      n.agent = agent;
      n.index = index;
      n.listed = true;
      n.selected = agent.id === selected;
      if (agent.status === 'done' && n.collapse < 0) n.collapse = 0;
      if (agent.status !== 'done') n.collapse = -1;
    });
    for (const [id, n] of nodes) {
      if (present.has(id)) continue;
      n.listed = false;
      if (n.collapse < 0) n.collapse = 0;
    }
    count = agents.length;
  };

  const tmp = new THREE.Vector3();

  const update = (dt: number, elapsed: number, motion: Motion): boolean => {
    let animating = false;
    const drift = elapsed * motionScale(motion);
    for (const [id, n] of [...nodes]) {
      const { agent } = n;
      const p = orbitPoint(orbitAngle(n.index, Math.max(1, count), drift, ORBIT.speed));
      tmp.set(p.x, p.y, p.z);
      let scale = n.selected ? SELECTED_SCALE : 1;
      if (n.collapse >= 0) {
        n.collapse += dt;
        const t = Math.min(1, n.collapse / COLLAPSE_S);
        tmp.lerp(core, t);
        scale *= 1 - t;
        if (t >= 1) {
          // A finished agent still on the roster stays collapsed; one that
          // left it is gone.
          if (!n.listed) {
            remove(id, n);
            continue;
          }
        } else {
          animating = true;
        }
      }
      n.body.position.copy(tmp);
      n.wire.position.copy(tmp);
      n.body.scale.setScalar(scale);
      n.wire.scale.setScalar(scale);
      if (agent.status === 'running' && motion !== 'off') {
        n.body.rotation.y += dt * SPIN * motionScale(motion);
        n.wire.rotation.y = n.body.rotation.y;
      }
      const failed = agent.status === 'failed';
      const base = failed ? colors.magenta : kindColor(agent);
      n.material.color.copy(base).multiplyScalar(BODY_INTENSITY);
      n.wireMaterial.color.copy(base);
      if (n.flashAge < FLASH_S) {
        n.flashAge += dt;
        n.material.color.lerp(n.flashColor, 1 - Math.min(1, n.flashAge / FLASH_S));
        animating = true;
      }
      const collapsed = n.collapse >= COLLAPSE_S;
      n.body.visible = !collapsed && agent.status !== 'provisioning';
      n.wire.visible = !collapsed && (agent.status === 'provisioning' || n.selected);
      n.material.opacity = agent.status === 'inactive' || agent.status === 'done' ? DIM : 1;
      if (agent.status === 'provisioning') {
        n.wireMaterial.opacity = 0.5 + 0.4 * Math.sin(elapsed * 4);
        if (motion !== 'off') animating = true;
      }
      if (failed && motion === 'full') {
        n.body.visible = n.body.visible && Math.floor(elapsed / FLICKER_S) % 7 !== 0;
        animating = true;
      }
      const line = n.filament.geometry.getAttribute('position') as THREE.BufferAttribute;
      line.setXYZ(1, tmp.x, tmp.y, tmp.z);
      line.needsUpdate = true;
      n.filament.visible = !collapsed;
      if (n.pulseAge < PULSE_S) {
        n.pulseAge += dt;
        const t = Math.min(1, n.pulseAge / PULSE_S);
        n.spark.visible = t < 1;
        n.spark.position.lerpVectors(core, tmp, t);
        n.sparkMaterial.opacity = 1 - t;
        n.filamentMaterial.opacity = 0.35 + 0.5 * (1 - t);
        animating = animating || t < 1;
      } else {
        n.spark.visible = false;
        n.filamentMaterial.opacity = 0.35;
      }
    }
    return animating;
  };

  const position = (id: string, out: THREE.Vector3): boolean => {
    const n = nodes.get(id);
    if (!n || n.collapse >= 0) return false;
    out.copy(n.body.position);
    return true;
  };

  const pulse = (id: string, color: THREE.Color) => {
    const n = nodes.get(id);
    if (!n) return;
    n.pulseAge = 0;
    n.sparkMaterial.color.copy(color);
  };

  const flash = (id: string, color: THREE.Color) => {
    const n = nodes.get(id);
    if (!n) return;
    n.flashAge = 0;
    n.flashColor.copy(color);
  };

  const dispose = () => {
    for (const [id, n] of [...nodes]) remove(id, n);
    prism.dispose();
    edges.dispose();
    sparkGeometry.dispose();
    group.clear();
  };

  return { group, sync, update, position, pulse, flash, ids: () => [...nodes.keys()], dispose };
}
