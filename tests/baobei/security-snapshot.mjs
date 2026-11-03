import { cpSync, mkdirSync } from 'node:fs';
import { resolve } from 'node:path';
const dest = resolve('local-private/baobei/security-snapshot');
mkdirSync(dest, { recursive: true });
for (const path of [
  'AGENTS.md',
  'tools/task.mjs',
  'apps/project-intake-web',
  'services/project-intake-api',
  'contracts/project-intake',
  'deploy/baobei',
  'tests/baobei',
  'docs/项目报备中心',
  '.github/workflows/baobei-ci.yml',
  '.github/workflows/baobei-publish.yml',
])
  cpSync(path, resolve(dest, path), {
    recursive: true,
    filter: (src) =>
      !src.split(/[\\/]/).some((p) => ['node_modules', 'dist', 'local-private', '__pycache__'].includes(p)),
  });
console.log('Product-only secret-scan snapshot prepared.');
