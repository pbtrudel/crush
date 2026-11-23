import type { AgentRun, Message, TeamMember, TeamState, TeamTask } from '../api/types';
import { needsAttention, type State } from './reducer';

export type AgentKind = 'main' | 'teammate' | 'subagent';
export type AgentStatus = 'running' | 'inactive' | 'provisioning' | 'failed' | 'done';

export interface Agent {
  sessionId: string;
  parentSessionId: string;
  name: string;
  kind: AgentKind;
  status: AgentStatus;
  model: string;
  modelProvider: string;
  startedAt: number;
  finishedAt: number;
  member?: string;
}

const LABELS: Record<AgentStatus, string> = {
  running: 'Running', inactive: 'Inactive', provisioning: 'Starting', failed: 'Failed', done: 'Done',
};

export function statusLabel(status: AgentStatus): string {
  return LABELS[status];
}

// sessionTree returns root plus every session reachable through parent to
// child links, like stateSessionTree in internal/ui/model/team.go.
export function sessionTree(root: string, links: Array<[string, string]>): Set<string> {
  const tree = new Set<string>();
  if (!root) return tree;
  tree.add(root);
  for (let changed = true; changed; ) {
    changed = false;
    for (const [parent, child] of links) {
      if (tree.has(parent) && !tree.has(child)) {
        tree.add(child);
        changed = true;
      }
    }
  }
  return tree;
}

const retained = (id: string) => id !== '' && !id.startsWith('title-') && !id.startsWith('helper-');
const time = (iso?: string) => (iso ? Date.parse(iso) || 0 : 0);

function memberStatus(m: TeamMember): AgentStatus {
  if (m.failed || m.status === 'failed') return 'failed';
  return m.status ?? 'inactive';
}

// freshest keeps one registry record per child session: the latest start,
// running winning a tie, as freshestAgentRuns does in the TUI.
function freshest(runs: AgentRun[]): AgentRun[] {
  const best = new Map<string, AgentRun>();
  for (const r of runs) {
    const key = r.session_id ?? '';
    const cur = best.get(key);
    const newer = !cur || time(r.started_at) > time(cur.started_at)
      || (time(r.started_at) === time(cur.started_at) && r.status === 'running' && cur.status !== 'running');
    if (newer) best.set(key, r);
  }
  return [...best.values()];
}

// byLiveness is sortAgentDescriptors in internal/ui/model/agent_discovery.go:
// running entries first with the oldest start, a start that was never
// recorded last, then settled entries newest finish first, and the session
// id breaks every tie.
function byLiveness(a: Agent, b: Agent): number {
  const ar = a.status === 'running' || a.status === 'provisioning';
  const br = b.status === 'running' || b.status === 'provisioning';
  if (ar !== br) return ar ? -1 : 1;
  if (ar) {
    if ((a.startedAt === 0) !== (b.startedAt === 0)) return a.startedAt === 0 ? 1 : -1;
    if (a.startedAt !== b.startedAt) return a.startedAt - b.startedAt;
  } else if (a.finishedAt !== b.finishedAt) {
    return b.finishedAt - a.finishedAt;
  }
  return a.sessionId < b.sessionId ? -1 : a.sessionId > b.sessionId ? 1 : 0;
}

export function deriveAgents(root: string, team: TeamState, runs: AgentRun[]): Agent[] {
  if (!root) return [];
  const links: Array<[string, string]> = [];
  for (const m of team.members) if (m.parent_session_id && m.session_id) links.push([m.parent_session_id, m.session_id]);
  for (const r of runs) if (r.parent_session_id && r.session_id) links.push([r.parent_session_id, r.session_id]);
  const tree = sessionTree(root, links);
  const out = new Map<string, Agent>();
  for (const m of team.members) {
    if (m.role === 'lead' || !m.session_id || !m.parent_session_id || !tree.has(m.parent_session_id)) continue;
    out.set(m.session_id, {
      sessionId: m.session_id, parentSessionId: m.parent_session_id, name: m.name, kind: 'teammate',
      status: memberStatus(m), model: m.model ?? '', modelProvider: m.model_provider ?? '',
      startedAt: 0, finishedAt: 0, member: m.name,
    });
  }
  for (const r of freshest(runs)) {
    if (!r.session_id || !r.parent_session_id || !tree.has(r.parent_session_id)) continue;
    const startedAt = time(r.started_at);
    const finishedAt = time(r.finished_at);
    const known = out.get(r.session_id);
    // A teammate's roster entry owns its status: a finished turn leaves it
    // inactive, not done, because it can be woken again.
    if (known) {
      out.set(r.session_id, { ...known, startedAt, finishedAt });
      continue;
    }
    out.set(r.session_id, {
      sessionId: r.session_id, parentSessionId: r.parent_session_id, name: r.name, kind: r.kind,
      status: r.status, model: '', modelProvider: '', startedAt, finishedAt,
      member: r.kind === 'teammate' ? r.name : undefined,
    });
  }
  return [...out.values()].filter((a) => retained(a.sessionId)).sort(byLiveness);
}

export function mainAgent(s: State): Agent {
  return {
    sessionId: s.sessionId, parentSessionId: '', name: 'Coder', kind: 'main',
    status: s.busy || needsAttention(s) ? 'running' : 'inactive',
    model: s.model.name || s.model.id, modelProvider: s.model.provider, startedAt: 0, finishedAt: 0,
  };
}

export function agentsOf(s: State): Agent[] {
  return [mainAgent(s), ...deriveAgents(s.sessionId, s.team, s.runs)];
}

export function findAgent(s: State, sid: string): Agent | null {
  return agentsOf(s).find((a) => a.sessionId === sid) ?? null;
}

// currentTool is the newest call of the newest assistant turn that has no
// result yet; `since` is when that turn last changed, in milliseconds.
export function currentTool(messages: Message[]): { name: string; since: number } | null {
  const done = new Set<string>();
  for (const m of messages) for (const p of m.parts) if (p.type === 'tool_result') done.add(p.data.tool_call_id);
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i];
    if (m.role !== 'assistant') continue;
    const finish = m.parts.find((p) => p.type === 'finish');
    if (finish && finish.type === 'finish' && finish.data.reason !== 'tool_use') return null;
    for (let j = m.parts.length - 1; j >= 0; j--) {
      const p = m.parts[j];
      if (p.type === 'tool_call' && !done.has(p.data.id)) return { name: p.data.name, since: m.updated_at * 1000 };
    }
    return null;
  }
  return null;
}

export function scopedTasks(root: string, team: TeamState): TeamTask[] {
  const links: Array<[string, string]> = [];
  for (const m of team.members) if (m.parent_session_id && m.session_id) links.push([m.parent_session_id, m.session_id]);
  const tree = sessionTree(root, links);
  return team.tasks.filter((t) => !!t.session_id && tree.has(t.session_id));
}

export function agentTask(team: TeamState, agent: Agent): TeamTask | null {
  const member = team.members.find((m) => m.session_id === agent.sessionId);
  if (!member) return null;
  const owned = team.tasks.filter((t) => t.owner_id === member.id);
  return owned.find((t) => t.status === 'in_progress') ?? owned.find((t) => t.status === 'pending') ?? null;
}
