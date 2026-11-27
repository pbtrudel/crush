import { describe, expect, it } from 'vitest';
import { LANGUAGES, languageExtension } from './languages';

describe('languages', () => {
  it('covers every language the gateway names', () => {
    // internal/web/files.go languageFor, without the plain text fallback.
    expect([...LANGUAGES].sort()).toEqual(['css', 'go', 'html', 'javascript', 'json', 'jsx', 'markdown', 'python', 'tsx', 'typescript', 'yaml']);
  });

  it('loads an extension for a known language and nothing for text', async () => {
    expect(await languageExtension('text')).toEqual([]);
    expect(await languageExtension('go')).not.toEqual([]);
  });
});
