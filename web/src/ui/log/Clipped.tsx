import { useState } from 'preact/hooks';

export const PREVIEW = 4000;
// Even "show full" stops here: a multi-megabyte output in one <pre> stalls
// layout for every later frame.
export const FULL_CAP = 256 * 1024;

export function Clipped({ text, label }: { text: string; label: string }) {
  const [full, setFull] = useState(false);
  const limit = full ? FULL_CAP : PREVIEW;
  const shown = text.length <= limit ? text : text.slice(0, limit);
  return (
    <>
      <pre>{shown}</pre>
      {!full && text.length > PREVIEW && (
        <button type="button" class="link-button" onClick={() => setFull(true)}>
          Show full {label} ({Math.round(text.length / 1024)} KB)
        </button>
      )}
      {full && text.length > FULL_CAP && (
        <p class="hud-label">Showing the first 256 KB; the terminal has the rest.</p>
      )}
    </>
  );
}
