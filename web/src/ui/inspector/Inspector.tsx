import type { Ref } from 'preact';
import { useEffect, useRef, useState } from 'preact/hooks';
import { agentTask, currentTool, findAgent, statusLabel, type Agent } from '../../state/agents';
import { actions, state } from '../../state/store';
import { Transcript } from '../log/Transcript';

const KIND = { main: 'Main agent', teammate: 'Teammate', subagent: 'Subagent' } as const;

function elapsed(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000));
  return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${String(s % 60).padStart(2, '0')}s`;
}

const fmtTokens = (n: number) => (n >= 1000 ? `${(n / 1000).toFixed(1)}k` : String(n));

export function Inspector({ panelRef }: { panelRef?: Ref<HTMLElement> } = {}) {
  const s = state.value;
  const ins = s.inspected;
  // The last agent seen for this selection. When the registry drops a
  // finished agent the inspector stays open on it, marked done.
  const known = useRef<Agent | null>(null);
  const found = ins ? findAgent(s, ins.sessionId) : null;
  if (found) known.current = found;
  else if (!ins) known.current = null;
  const agent = found ?? (known.current && ins && known.current.sessionId === ins.sessionId ? { ...known.current, status: 'done' as const } : null);
  // The roster can be pruned before this selection is ever rendered, so a
  // selection with neither a found nor a remembered agent says so instead
  // of leaving a blank panel.
  const gone = !!ins && !agent;
  const open = !!(ins && (agent || gone));

  const main = !!ins && ins.sessionId === s.sessionId;
  const messages = main ? s.messages : ins?.messages ?? [];
  const session = main ? s.session : ins?.session ?? null;
  const tool = currentTool(messages);
  const task = agent ? agentTask(s.team, agent) : null;
  const running = agent?.status === 'running' || agent?.status === 'provisioning';

  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!tool) return;
    setNow(Date.now());
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [tool?.name, tool?.since]);

  const [text, setText] = useState('');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  useEffect(() => {
    setText('');
    setError('');
    setNotice('');
  }, [ins?.sessionId]);

  const title = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    // A pick in the Agents list keeps focus there, so the user can arrow
    // through agents; a pick anywhere else moves focus to the inspector.
    if (open && !(document.activeElement instanceof HTMLElement && document.activeElement.closest('.agent-list'))) title.current?.focus();
  }, [ins?.sessionId, open]);

  const run = async (fn: () => Promise<string | null>, done: string) => {
    setError('');
    setNotice('');
    const err = await fn();
    if (err) setError(err);
    else setNotice(done);
  };

  const close = () => {
    const sid = ins?.sessionId;
    void actions.inspect(null);
    if (sid !== undefined) document.getElementById(`agent-${sid || 'main'}`)?.focus();
  };

  const onKeyDown = (e: KeyboardEvent) => {
    if (e.key !== 'Escape' || e.isComposing) return;
    e.preventDefault();
    e.stopPropagation();
    close();
  };

  return (
    <section ref={panelRef} class="panel panel--inspector glass scanlines" aria-labelledby="inspector-title" hidden={!open} onKeyDown={onKeyDown}>
      {open && ins && (
        <>
          <header class="inspector__head">
            <span class="hud-label">{agent ? KIND[agent.kind] : 'Agent'}</span>
            <h2 id="inspector-title" ref={title} tabIndex={-1}>{agent ? agent.name : 'Agent gone'}</h2>
            <button type="button" class="btn btn--ghost inspector__close" aria-label="Close inspector" onClick={close}>×</button>
          </header>
          {agent ? (
            <>
              <dl class="inspector__facts">
                <div data-fact="status"><dt class="hud-label">Status</dt><dd data-status={agent.status}>{statusLabel(agent.status)}</dd></div>
                <div data-fact="tool"><dt class="hud-label">Current tool</dt><dd>{tool ? `${tool.name} · ${elapsed(now - tool.since)}` : 'None'}</dd></div>
                <div data-fact="model"><dt class="hud-label">Model</dt><dd>{agent.model ? `${agent.model}${agent.modelProvider ? ` · ${agent.modelProvider}` : ''}` : 'Not reported'}</dd></div>
                <div data-fact="usage"><dt class="hud-label">Tokens</dt><dd>{session ? `${fmtTokens(session.prompt_tokens + session.completion_tokens)} · $${session.cost.toFixed(2)}` : 'Not reported'}</dd></div>
                {task && <div data-fact="task"><dt class="hud-label">Task</dt><dd>{task.subject} <span class="hud-label">{task.status === 'in_progress' ? 'In progress' : 'Pending'}</span></dd></div>}
              </dl>
              {agent.kind !== 'main' && (
                <div class="inspector__actions">
                  {agent.kind === 'teammate' && running && (
                    <button type="button" class="btn btn--danger" data-action="interrupt" onClick={() => void run(() => actions.interruptTeammate(agent.member ?? agent.name), `Interrupted ${agent.name}`)}>Interrupt</button>
                  )}
                  {agent.kind === 'subagent' && running && (
                    <button type="button" class="btn btn--danger" data-action="cancel" onClick={() => void run(() => actions.cancelAgent(agent.sessionId), `Cancelled ${agent.name}`)}>Cancel</button>
                  )}
                  {agent.kind === 'teammate' && agent.status !== 'done' && (
                    <form class="inspector__message" onSubmit={(e) => {
                      e.preventDefault();
                      const body = text.trim();
                      if (!body) return;
                      void run(async () => {
                        const err = await actions.messageTeammate(agent.member ?? agent.name, body);
                        if (!err) setText('');
                        return err;
                      }, `Sent to ${agent.name}`);
                    }}>
                      <label class="hud-label" for="inspector-message">Message {agent.name}</label>
                      <textarea id="inspector-message" rows={2} value={text} onInput={(e) => setText((e.currentTarget as HTMLTextAreaElement).value)} />
                      <button type="submit" class="btn btn--primary" data-action="message">Send</button>
                    </form>
                  )}
                  {error && <p class="inspector__error" role="alert">{error}</p>}
                  <p class="hud-label inspector__notice" role="status">{notice}</p>
                </div>
              )}
              {ins.loading
                ? <p class="hud-label">Loading the transcript</p>
                : <Transcript messages={messages} resetKey={ins.sessionId} label={`${agent.name} transcript`} empty={<p class="hud-label">No messages yet</p>} />}
            </>
          ) : (
            <p class="hud-label inspector__gone">This agent is no longer in the roster.</p>
          )}
        </>
      )}
    </section>
  );
}
