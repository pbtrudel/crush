import { useState } from 'preact/hooks';
import type { ToolResultData } from '../../api/types';
import { summarizeTool } from './toolSummary';

const PREVIEW = 4000;

function pretty(input: string): string {
  try {
    return JSON.stringify(JSON.parse(input), null, 2);
  } catch {
    return input;
  }
}

export function ToolCard({ call, result }: {
  call: { id: string; name: string; input: string; finished?: boolean };
  result?: ToolResultData;
}) {
  const [full, setFull] = useState(false);
  const status = result ? (result.is_error ? 'error' : 'done') : 'running';
  const content = result?.content ?? '';
  const shown = full || content.length <= PREVIEW ? content : content.slice(0, PREVIEW);
  return (
    <details class="tool" data-status={status}>
      <summary>
        <span class="tool__glyph" aria-hidden="true">{status === 'error' ? '✕' : status === 'done' ? '◆' : '◇'}</span>
        <span class="tool__name">{call.name}</span>
        <span class="tool__summary">{summarizeTool(call.name, call.input)}</span>
        <span class="hud-label tool__status">{status === 'running' ? 'Running' : status === 'error' ? 'Failed' : 'Done'}</span>
      </summary>
      <div class="tool__body">
        <h4 class="hud-label">Input</h4>
        <pre>{pretty(call.input)}</pre>
        {result && (
          <>
            <h4 class="hud-label">Output</h4>
            <pre>{shown}</pre>
            {shown.length < content.length && (
              <button type="button" class="link-button" onClick={() => setFull(true)}>
                Show full output ({Math.round(content.length / 1024)} KB)
              </button>
            )}
          </>
        )}
      </div>
    </details>
  );
}
