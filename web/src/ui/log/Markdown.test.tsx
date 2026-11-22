import { afterEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import DOMPurify from 'dompurify';
import { Markdown } from './Markdown';

function renderText(text: string): HTMLDivElement {
  const root = document.createElement('div');
  render(<Markdown text={text} />, root);
  return root;
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('Markdown', () => {
  it('renders markdown and strips scripts and handlers', () => {
    const root = renderText('**bold** <img src=x onerror="alert(1)"><script>alert(2)</script>');
    expect(root.querySelector('strong')?.textContent).toBe('bold');
    expect(root.innerHTML).not.toContain('onerror');
    expect(root.innerHTML).not.toContain('<script');
  });
  it('opens links safely', () => {
    const root = renderText('[site](https://charm.land)');
    const a = root.querySelector('a')!;
    expect(a.getAttribute('target')).toBe('_blank');
    expect(a.getAttribute('rel')).toBe('noopener noreferrer');
  });
  it('drops inline style attributes the page CSP would block', () => {
    const root = renderText('<span style="color:red">x</span>');
    expect(root.querySelector('span')?.textContent).toBe('x');
    expect(root.querySelector('span')?.hasAttribute('style')).toBe(false);
    expect(root.innerHTML).not.toContain('style=');
  });
  it('drops style, form and input elements', () => {
    const root = renderText('<style>body{color:red}</style><form><input value="x"></form>');
    expect(root.innerHTML).not.toContain('<style');
    expect(root.innerHTML).not.toContain('<form');
    expect(root.innerHTML).not.toContain('<input');
  });
  it('strips javascript URLs', () => {
    const root = renderText('[click](javascript:alert(1))');
    const href = root.querySelector('a')?.getAttribute('href') ?? '';
    expect(href.toLowerCase()).not.toContain('javascript:');
  });
  it('turns a remote image into a link', () => {
    const root = renderText('![chart](https://example.com/c.png)');
    expect(root.querySelector('img')).toBeNull();
    const a = root.querySelector('a')!;
    expect(a.getAttribute('href')).toBe('https://example.com/c.png');
    expect(a.textContent).toBe('Image: chart');
  });
  it('forbids every element that can carry a remote image', () => {
    const root = renderText(
      '<img srcset="https://e/x.png 1x">'
      + '<picture><source srcset="https://e/x.png"></picture>'
      + '<svg><image href="https://e/x.png"></image></svg>',
    );
    expect(root.querySelector('source')).toBeNull();
    expect(root.querySelector('picture')).toBeNull();
    expect(root.querySelector('image')).toBeNull();
    expect(root.querySelector('[srcset]')).toBeNull();
    expect(root.innerHTML).not.toContain('https://e/');
  });
  it('links an image source only when it is http or https', () => {
    const https = renderText('![x](https://example.com/x.png)');
    expect(https.querySelector('a')?.getAttribute('href')).toBe('https://example.com/x.png');
    expect(https.querySelector('a')?.getAttribute('rel')).toBe('noopener noreferrer');
    const ftp = renderText('![x](ftp://example.com/x.png)');
    expect(ftp.querySelector('a')).toBeNull();
    const js = renderText('![x](javascript:alert(1))');
    expect(js.querySelector('a')).toBeNull();
    expect(js.innerHTML.toLowerCase()).not.toContain('javascript:');
    const data = renderText('![x](data:image/png;base64,AAAA)');
    expect(data.querySelector('a')).toBeNull();
    expect(data.querySelector('img')?.getAttribute('src')).toBe('data:image/png;base64,AAAA');
  });
  it('does not re-parse unchanged text on re-render', () => {
    const spy = vi.spyOn(DOMPurify, 'sanitize');
    const root = document.createElement('div');
    render(<Markdown text="stable" />, root);
    render(<Markdown text="stable" />, root);
    expect(spy).toHaveBeenCalledTimes(1);
  });
});
