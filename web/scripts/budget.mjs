import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { gzipSync } from 'node:zlib';

// The browser downloads these before the page can render. Lazy chunks such
// as the three.js scene, the cinematic flight and OrbitControls are not
// referenced here, so they never count against the budget.
const dist = fileURLToPath(new URL('../../internal/web/dist/', import.meta.url));
const LIMIT = 600 * 1024;

const html = readFileSync(path.join(dist, 'index.html'), 'utf8');
const refs = new Set();
for (const tag of html.match(/<(?:script|link)\b[^>]*>/g) ?? []) {
  const src = /\ssrc="([^"]+)"/.exec(tag)?.[1];
  const href = /\shref="([^"]+)"/.exec(tag)?.[1];
  const rel = (/\srel="([^"]+)"/.exec(tag)?.[1] ?? '').split(/\s+/);
  if (tag.startsWith('<script') && src) refs.add(src);
  if (tag.startsWith('<link') && href && (rel.includes('stylesheet') || rel.includes('modulepreload'))) {
    refs.add(href);
  }
}

let total = 0;
for (const ref of refs) {
  const file = path.join(dist, ref.replace(/^\//, ''));
  const compressed = gzipSync(readFileSync(file)).byteLength;
  total += compressed;
  console.log(`${ref.replace(/^\//, '')}  ${compressed} bytes gzip`);
}

console.log(`Total eager gzip: ${total} bytes (limit ${LIMIT})`);
if (total > LIMIT) {
  console.error(`Eager bundle is ${total - LIMIT} bytes over the ${LIMIT} byte budget`);
  process.exitCode = 1;
}
