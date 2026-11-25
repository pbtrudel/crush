import type { State } from './reducer';
import { summarizeTool } from '../ui/log/toolSummary';
import { toolCategory, type ToolCategory } from './toolCategory';

export interface ChronoEvent {
  callId: string;
  sessionId: string;
  tool: string;
  category: ToolCategory;
  at: number;
  label: string;
  edit: boolean;
  childSessionId?: string;
}

// The synchronous agent tool names its child session after the message and
// the call (internal/session's CreateAgentToolSessionID), so the roster is
// the only place a browser can learn that session exists.
const AGENT_TOOL = 'agent';

// chronoEvents derives the timeline from the messages the tab already holds:
// the newest cap tool calls of the shown session, oldest first.
export function chronoEvents(s: State, cap = 200): ChronoEvent[] {
  if (cap <= 0) return [];
  const known = new Set<string>();
  for (const run of s.runs) if (run.session_id) known.add(run.session_id);
  for (const member of s.team.members) {
    if (member.session_id && member.role !== 'lead') known.add(member.session_id);
  }
  const events: ChronoEvent[] = [];
  // Walk backwards so hundreds of calls cost only the newest ones, then
  // restore the oldest-first order the strip draws.
  for (let i = s.messages.length - 1; i >= 0 && events.length < cap; i--) {
    const message = s.messages[i];
    if (message.session_id !== s.sessionId) continue;
    for (let j = message.parts.length - 1; j >= 0 && events.length < cap; j--) {
      const part = message.parts[j];
      if (part.type !== 'tool_call') continue;
      const category = toolCategory(part.data.name);
      const child = part.data.name === AGENT_TOOL ? `${message.id}$$${part.data.id}` : '';
      events.push({
        callId: part.data.id,
        sessionId: message.session_id,
        tool: part.data.name,
        category,
        at: message.created_at,
        label: summarizeTool(part.data.name, part.data.input),
        edit: category === 'write',
        ...(child && known.has(child) ? { childSessionId: child } : {}),
      });
    }
  }
  return events.reverse();
}
