import { useMemo } from 'preact/hooks';
import { marked } from 'marked';
import DOMPurify from 'dompurify';

// The page CSP allows style-src 'self', so an inline style attribute would
// never apply; drop it here instead of shipping dead markup. Forms, form
// controls, and the elements and attributes that fetch an image by another
// route (picture, source, svg image, srcset) are not part of a transcript
// either. The hook is registered once per module load, not on every render.
const SANITIZE = {
  FORBID_ATTR: ['style', 'srcset'],
  FORBID_TAGS: ['style', 'form', 'input', 'picture', 'source', 'image'],
};

// The page CSP allows img-src data: and blob: only.
export const isLocalUrl = (u: string) => u.startsWith('data:') || u.startsWith('blob:');
const isHttpUrl = (u: string) => /^https?:\/\//i.test(u);

DOMPurify.addHook('afterSanitizeAttributes', (node) => {
  if (node.tagName === 'A') {
    node.setAttribute('target', '_blank');
    node.setAttribute('rel', 'noopener noreferrer');
  }
  // A remote image would render as a broken box, so keep the reference as
  // a link. Only an http(s) source becomes one, so no other scheme can ever
  // reach an href.
  if (node.tagName === 'IMG') {
    const src = node.getAttribute('src') ?? '';
    if (isHttpUrl(src)) {
      const a = node.ownerDocument.createElement('a');
      a.setAttribute('href', src);
      a.setAttribute('target', '_blank');
      a.setAttribute('rel', 'noopener noreferrer');
      a.textContent = `Image: ${node.getAttribute('alt') || src}`;
      node.replaceWith(a);
    }
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
