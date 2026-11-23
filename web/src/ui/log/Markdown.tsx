import { useMemo } from 'preact/hooks';
import { marked } from 'marked';
import DOMPurify from 'dompurify';

// The page CSP allows style-src 'self', so an inline style attribute would
// never apply; drop it here instead of shipping dead markup. Forms and form
// controls are not part of a transcript either. The hook is registered once
// per module load, not on every render.
const SANITIZE = {
  FORBID_ATTR: ['style'],
  FORBID_TAGS: ['style', 'form', 'input'],
};

DOMPurify.addHook('afterSanitizeAttributes', (node) => {
  if (node.tagName === 'A') {
    node.setAttribute('target', '_blank');
    node.setAttribute('rel', 'noopener noreferrer');
  }
});

export function Markdown({ text }: { text: string }) {
  // Streaming appends to the last message only, so memoizing on the text
  // keeps every unchanged message from being parsed again.
  const html = useMemo(
    () => DOMPurify.sanitize(marked.parse(text, { async: false, gfm: true, breaks: false }), SANITIZE),
    [text],
  );
  return <div class="md" dangerouslySetInnerHTML={{ __html: html }} />;
}
