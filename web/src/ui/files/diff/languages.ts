import type { Extension } from '@codemirror/state';

// One dynamic import per language, so a Go diff never downloads the HTML
// grammar. The names match languageFor in internal/web/files.go.
const LOADERS: Record<string, () => Promise<Extension>> = {
  go: async () => (await import('@codemirror/lang-go')).go(),
  typescript: async () => (await import('@codemirror/lang-javascript')).javascript({ typescript: true }),
  tsx: async () => (await import('@codemirror/lang-javascript')).javascript({ typescript: true, jsx: true }),
  javascript: async () => (await import('@codemirror/lang-javascript')).javascript(),
  jsx: async () => (await import('@codemirror/lang-javascript')).javascript({ jsx: true }),
  json: async () => (await import('@codemirror/lang-json')).json(),
  css: async () => (await import('@codemirror/lang-css')).css(),
  html: async () => (await import('@codemirror/lang-html')).html(),
  markdown: async () => (await import('@codemirror/lang-markdown')).markdown(),
  python: async () => (await import('@codemirror/lang-python')).python(),
  yaml: async () => (await import('@codemirror/lang-yaml')).yaml(),
};

export const LANGUAGES = Object.keys(LOADERS);

export async function languageExtension(name: string): Promise<Extension> {
  const load = LOADERS[name];
  if (!load) return [];
  try {
    return await load();
  } catch {
    // A grammar that fails to load leaves the diff readable as plain text.
    return [];
  }
}
