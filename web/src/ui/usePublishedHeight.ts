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
