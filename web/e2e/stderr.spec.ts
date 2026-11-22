import { expect, test } from '@playwright/test';
import { stderrBuffer } from './stderr';

test('redacts a token split across chunks', () => {
  const b = stderrBuffer(64);
  b.push('Dev server: open http://localhost:5173/#t=abc');
  b.push('def123\nnext line\n');
  expect(b.text()).not.toContain('def123');
  expect(b.text()).toContain('#t=<redacted>');
});

test('keeps only the tail and never a token fragment', () => {
  const b = stderrBuffer(32);
  for (let i = 0; i < 10; i++) b.push(`line ${i} #t=SECRET${i}\n`);
  expect(b.text()).not.toMatch(/SECRET/);
  expect(b.text().length).toBeLessThanOrEqual(32);
});

test('redacts a partial last line', () => {
  const b = stderrBuffer(64);
  b.push('boom #t=HALF');
  expect(b.text()).toBe('boom #t=<redacted>');
});

test('drops an oversized line until its newline', () => {
  const b = stderrBuffer(64);
  b.push('x'.repeat(100) + ' #t=AB');
  b.push('CDEF\nnext line\n');
  expect(b.text()).not.toContain('AB');
  expect(b.text()).not.toContain('CDEF');
  expect(b.text()).toContain('next line');
});

test('resumes after a dropped line that ends in the same chunk as more lines', () => {
  const b = stderrBuffer(64);
  b.push('w'.repeat(100) + ' #t=');
  b.push('ZZZtail\nkept line\n');
  expect(b.text()).toBe('kept line');
});
