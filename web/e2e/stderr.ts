// The launch line carries the session token. Only whole lines are kept,
// redacted as they complete, so a token split across two chunks or cut by
// the size cap never reaches the buffer in part.
export function redact(text: string): string {
  return text.replace(/#t=\S+/g, '#t=<redacted>');
}

export function stderrBuffer(limit = 4096) {
  let kept = '';
  let pending = '';
  return {
    push(chunk: string) {
      pending += chunk;
      const end = pending.lastIndexOf('\n');
      if (end >= 0) {
        kept = (kept + redact(pending.slice(0, end + 1))).slice(-limit);
        pending = pending.slice(end + 1);
      }
      // A line that never ends is dropped whole rather than cut, since a
      // cut could keep the tail of a token without its #t= marker.
      if (pending.length > limit) pending = '';
    },
    text(): string {
      return (kept + redact(pending)).trim().slice(-limit);
    },
  };
}
