import { useEffect } from 'preact/hooks';

// usePublishedHeight publishes an element's measured height as a CSS custom
// property on the document root, so a fixed surface can be placed from it
// instead of a hard-coded offset. The property is removed when the element
// leaves the tree.
export function usePublishedHeight(ref: { current: HTMLElement | null }, property: string): void {
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const doc = document.documentElement;
    const publish = () => {
      const height = Math.round(el.getBoundingClientRect().height);
      if (height > 0) doc.style.setProperty(property, `${height}px`);
    };
    publish();
    const observer = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(publish);
    observer?.observe(el);
    return () => {
      observer?.disconnect();
      doc.style.removeProperty(property);
    };
  }, [ref, property]);
}

// usePublishedTop publishes an element's viewport top as a CSS custom
// property on the document root. A surface pinned to an element that can
// overflow the shell needs the measured edge: the viewport and the element's
// height do not describe where it starts. A resize moves the edge without
// resizing the element, so both are watched.
export function usePublishedTop(ref: { current: HTMLElement | null }, property: string): void {
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const doc = document.documentElement;
    const publish = () => {
      doc.style.setProperty(property, `${Math.round(el.getBoundingClientRect().top)}px`);
    };
    publish();
    window.addEventListener('resize', publish);
    const observer = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(publish);
    observer?.observe(el);
    return () => {
      observer?.disconnect();
      window.removeEventListener('resize', publish);
      doc.style.removeProperty(property);
    };
  }, [ref, property]);
}
