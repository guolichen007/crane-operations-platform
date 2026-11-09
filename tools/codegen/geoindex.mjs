// Generates the backend administrative-division index from the single canonical source
// apps/web-console/public/geo/geo-manifest.json (the file the browser loads).
// Output: internal/geo/index.gen.json  = { version: sha256(source bytes), source, regions: [[adcode,parent,level,name], ...] }
// The Go package internal/geo embeds that file; `--check` fails on any drift, and the index version is
// returned to clients as scope.geoIndexVersion so browser and backend can prove they use the same table.
import { createHash } from 'node:crypto';
import { readFile, writeFile } from 'node:fs/promises';

const SOURCE = 'apps/web-console/public/geo/geo-manifest.json';
const TARGET = 'internal/geo/index.gen.json';
const LEVEL = { country: 'C', province: 'P', prefecture: 'M', county: 'X' };

const raw = await readFile(SOURCE);
const manifest = JSON.parse(raw.toString('utf8'));
const regions = Object.entries(manifest)
  .map(([key, r]) => {
    if (String(r.adcode) !== key) throw Error(`manifest key ${key} != adcode ${r.adcode}`);
    if (!LEVEL[r.level]) throw Error(`unknown level ${r.level} for ${key}`);
    return [r.adcode, r.parent ?? 0, LEVEL[r.level], r.name];
  })
  .sort((a, b) => a[0] - b[0]);
const text =
  JSON.stringify({ version: createHash('sha256').update(raw).digest('hex'), source: SOURCE, regions }).replace(/\],\[/g, '],\n[') + '\n';
if (process.argv.includes('--check')) {
  if ((await readFile(TARGET, 'utf8').catch(() => '')) !== text) {
    console.error(`Geo index drift: ${TARGET} is not generated from ${SOURCE}; run node tools/codegen/geoindex.mjs`);
    process.exitCode = 1;
  }
} else await writeFile(TARGET, text);
