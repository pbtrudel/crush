// First-paint JavaScript budget: the gzipped size of every module script
// the built index.html loads must stay under 600 KiB.
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { gzipSync } from 'node:zlib'

const budgetBytes = 600 * 1024
const here = dirname(fileURLToPath(import.meta.url))
const dist = join(here, '..', '..', 'internal', 'web', 'dist')

const html = readFileSync(join(dist, 'index.html'), 'utf8')
// A module preload is first-paint JavaScript too: the browser fetches and
// parses it before the first paint even though the tag is a link.
const links = [...html.matchAll(/<link[^>]+rel="modulepreload"[^>]*href="([^"]+)"/g)].map((match) => match[1])
const scripts = [...html.matchAll(/<script[^>]+src="([^"]+)"/g)].map((match) => match[1])
const sources = [...new Set([...scripts, ...links])]
if (scripts.length === 0) {
  console.error('budget: the built index.html loads no module script')
  process.exit(1)
}

let total = 0
for (const source of sources) {
  const file = join(dist, source.replace(/^\//, ''))
  const gzipped = gzipSync(readFileSync(file)).length
  total += gzipped
  console.log(`${source}: ${(gzipped / 1024).toFixed(1)} KiB gzip`)
}
console.log(`first paint: ${(total / 1024).toFixed(1)} KiB gzip of ${budgetBytes / 1024} KiB`)
if (total > budgetBytes) {
  console.error(`budget: over the ${budgetBytes / 1024} KiB cap`)
  process.exit(1)
}
