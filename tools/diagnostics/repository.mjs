import { readdir, readFile, stat } from 'node:fs/promises';
import { join, relative } from 'node:path';
const ignored = new Set([
  '.git',
  'node_modules',
  'local-private',
  'release',
  'runtime',
  '.cache',
  'dist',
  'dist-offline',
  'playwright-report',
]);
const blocked = [
  /-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----/,
  /(?:AKIA|ASIA)[0-9A-Z]{16}/,
  /gh[pousr]_[A-Za-z0-9]{30,}/,
];
let count = 0;
async function walk(dir = '.') {
  for (const item of await readdir(dir, { withFileTypes: true })) {
    if (ignored.has(item.name) || item.name.startsWith('test-results')) continue;
    const path = join(dir, item.name);
    if (item.isDirectory()) {
      await walk(path);
      continue;
    }
    if (
      /\.(?:key|pem|crt|p12|pfx)$/.test(item.name) ||
      (/^\.env(?:\.|$)/.test(item.name) && item.name !== '.env.example')
    )
      throw Error(`Forbidden material: ${path}`);
    if ((await stat(path)).size > 15e6) throw Error(`Unexpected large file: ${path}`);
    const text = await readFile(path, 'utf8');
    for (const pattern of blocked)
      if (pattern.test(text)) throw Error(`Potential credential: ${relative('.', path)}`);

    count++;
  }
}
await walk();
console.log(`Repository safety precheck: ${count} files. Run gitleaks for full secret scan.`);
