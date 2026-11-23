import { useEffect, useRef, useState } from 'preact/hooks';
import type { CommandArg } from '../../api/types';

export interface ArgumentFormProps {
  title: string;
  args: CommandArg[];
  onSubmit: (values: Record<string, string>) => void;
  onCancel: () => void;
}

// ArgumentForm fills a command's declared arguments before the palette runs
// it. Required fields block the submit with an inline message; Escape hands
// the list back, which the App's capture listener routes here.
export function ArgumentForm({ title, args, onSubmit, onCancel }: ArgumentFormProps) {
  const [values, setValues] = useState<Record<string, string>>(() =>
    Object.fromEntries(args.map((a) => [a.id, ''])));
  const [error, setError] = useState('');
  const first = useRef<HTMLInputElement>(null);

  useEffect(() => {
    first.current?.focus();
  }, []);

  const submit = (e: Event) => {
    e.preventDefault();
    const missing = args.filter((a) => a.required && !(values[a.id] ?? '').trim());
    if (missing.length > 0) {
      setError(`${missing.map((a) => a.title).join(', ')} is required.`);
      return;
    }
    const out: Record<string, string> = {};
    for (const a of args) {
      const value = (values[a.id] ?? '').trim();
      if (value) out[a.id] = value;
    }
    setError('');
    onSubmit(out);
  };

  return (
    <form
      class="palette__form"
      aria-label={`Arguments for ${title}`}
      onSubmit={submit}
      onKeyDown={(e) => {
        if (e.key === 'Escape') {
          e.preventDefault();
          onCancel();
        }
      }}
    >
      <p class="palette__form-title hud-label">{title}</p>
      {args.map((a, i) => (
        <label class="palette__field" key={a.id}>
          <span class="hud-label">{a.title}{a.required ? ' (required)' : ''}</span>
          {a.description && <span class="palette__hint">{a.description}</span>}
          <input
            id={`argument-${a.id}`}
            ref={i === 0 ? first : undefined}
            class="palette__input"
            value={values[a.id] ?? ''}
            required={a.required}
            onInput={(e) => {
              const value = (e.currentTarget as HTMLInputElement).value;
              setValues((cur) => ({ ...cur, [a.id]: value }));
            }}
          />
        </label>
      ))}
      <p class="palette__error" role="alert">{error}</p>
      <div class="palette__actions">
        <button type="submit" class="btn btn--primary">Run</button>
        <button type="button" class="btn btn--ghost" onClick={onCancel}>Cancel</button>
      </div>
    </form>
  );
}
