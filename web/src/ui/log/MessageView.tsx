import type { Message, Part, ToolResultData } from '../../api/types';
import { Markdown } from './Markdown';
import { ToolCard } from './ToolCard';

type FinishPart = Extract<Part, { type: 'finish' }>;

export function MessageView({ m, results }: { m: Message; results: Map<string, ToolResultData> }) {
  if (m.role === 'tool') return null;
  const finish = m.parts.find((p): p is FinishPart => p.type === 'finish');
  const streaming = m.role === 'assistant' && !finish;
  // A tool call with no result stays running while the turn is open. Once the
  // turn is finished it was canceled or interrupted, unless the finish reason
  // is tool_use, which is the normal moment tools start executing.
  const settled = finish !== undefined && finish.data.reason !== 'tool_use';
  return (
    <article class="msg" data-role={m.role} data-streaming={streaming}>
      <header class="hud-label msg__who">{m.role === 'user' ? 'You' : m.role === 'assistant' ? 'Coder' : 'System'}</header>
      {m.parts.map((p, i) => {
        switch (p.type) {
          case 'reasoning':
            return p.data.thinking ? (
              <details class="thought" key={i}>
                <summary class="hud-label">Thought layer</summary>
                <Markdown text={p.data.thinking} />
              </details>
            ) : null;
          case 'text':
            return p.data.hidden ? null : <Markdown key={i} text={p.data.text} />;
          case 'tool_call':
            return (
              <ToolCard
                key={p.data.id}
                call={p.data}
                result={results.get(p.data.id)}
                settled={settled}
              />
            );
          case 'image_url':
            return <img key={i} class="msg__image" src={p.data.url} alt="Attached image" />;
          case 'binary':
            return <p key={i} class="msg__attachment">Attachment: {p.data.path ?? p.data.mime_type ?? 'file'}</p>;
          case 'shell_command':
            return (
              <div key={i} class="shell">
                <code>$ {p.data.command}</code>
                <pre>{p.data.output}</pre>
                <span class="hud-label">Exit {p.data.exit_code}</span>
              </div>
            );
          case 'compaction_stats':
            return <p key={i} class="msg__marker hud-label">Context compacted, saved {p.data.saved_tokens} tokens</p>;
          case 'sub_agent_report':
            return (
              <section key={i} class="report" data-failed={p.data.failed ?? false}>
                <h4 class="hud-label">{p.data.label}</h4>
                <Markdown text={p.data.output} />
              </section>
            );
          case 'finish':
            return p.data.reason === 'error' || p.data.reason === 'canceled' ? (
              <p key={i} class="msg__finish" data-reason={p.data.reason}>
                {p.data.reason === 'canceled' ? 'Canceled.' : `Error: ${p.data.message ?? 'the turn failed'}`}
              </p>
            ) : null;
          default:
            return null;
        }
      })}
      {streaming && <span class="msg__cursor" aria-hidden="true" />}
    </article>
  );
}
