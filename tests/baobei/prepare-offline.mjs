import { mkdirSync, copyFileSync, existsSync, readFileSync, writeFileSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
const cache = process.env.GOMODCACHE;
if (!cache) throw Error('GOMODCACHE required');
const escape = (s) => s.replace(/[A-Z]/g, (c) => '!' + c.toLowerCase());
const modules = new Map();
for (const line of readFileSync('services/project-intake-api/go.sum', 'utf8').trim().split('\n')) {
  const [path, version] = line.split(' ');
  modules.set(path + '@' + version, {
    path,
    version: version.replace('/go.mod', ''),
    modOnly: version.endsWith('/go.mod'),
  });
}
for (const { path, version, modOnly } of modules.values()) {
  const rel = escape(path) + '/@v/' + escape(version);
  for (const ext of modOnly ? ['.mod'] : ['.mod', '.zip', '.ziphash', '.info']) {
    const src = resolve(cache, 'cache/download', rel + ext),
      dst = resolve('local-private/baobei/go-download-cache', rel + ext);
    mkdirSync(dirname(dst), { recursive: true });
    if (existsSync(src)) copyFileSync(src, dst);
    else if (ext === '.info')
      writeFileSync(dst, JSON.stringify({ Version: version, Time: '2026-01-01T00:00:00Z' }));
    else throw Error('Missing cached public module ' + path + ext);
  }
}
console.log('Offline public module cache prepared; go.sum verifies all selected module content.');
