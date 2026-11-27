// The launch line carries the session token. Only whole lines are kept,
// redacted as they complete, so a token split across two chunks or cut by
// the size cap never reaches the buffer in part.
export function redact(text: string): string {
  return text.replace(/#t=\S+/g, '#t=<redacted>');
}

export function stderrBuffer(limit = 4096) {
  let kept = '';
  let pending = '';
  let dropping = false;
  return {
    push(chunk: string) {
      if (dropping) {
        // The rest of the line still belongs to the dropped line, so skip
        // it up to the newline and process only what follows.
        const newline = chunk.indexOf('\n');
        if (newline < 0) return;
        chunk = chunk.slice(newline + 1);
        dropping = false;
      }
      pending += chunk;
      const end = pending.lastIndexOf('\n');
      if (end >= 0) {
        kept = (kept + redact(pending.slice(0, end + 1))).slice(-limit);
        pending = pending.slice(end + 1);
      }
      // An unterminated line longer than the cap is dropped whole, and so
      // is the rest of it as it arrives. Cutting it could keep the tail of
      // a token without its #t= marker, and the tail of a dropped line can
      // carry a token whose marker fell in the part already discarded.
      if (pending.length > limit) {
        pending = '';
        dropping = true;
      }
    },
    text(): string {
      return (kept + redact(pending)).trim().slice(-limit);
    },
  };
}
