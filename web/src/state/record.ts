// without returns the record without one key. A record that never held the
// key comes back as the same object, so a caller that stores the answer does
// not re-render for nothing.
export function without<R extends Record<string, unknown>>(record: R, key: string): R {
  if (!(key in record)) return record;
  const next = { ...record };
  delete next[key];
  return next;
}
