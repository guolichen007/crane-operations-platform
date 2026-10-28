import { spawnSync } from 'node:child_process';
import { realpathSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

// Bind deployment tools and Compose to this script's checkout, never the caller's cwd.
export const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
export function assertExactCheckout(sourceSHA, root = repositoryRoot) {
  if (!/^[a-f0-9]{40}$/.test(sourceSHA || '')) throw Error('Full release source SHA required');
  const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !/^GIT_/i.test(key)));
  env.GIT_OPTIONAL_LOCKS = '0';
  function git(...args) {
    const result = spawnSync('git', ['-C', root, ...args], { env, encoding: 'utf8' });
    if (result.error || result.status !== 0) throw Error('Cannot verify deployment Git checkout');
    return result.stdout.trim();
  }
  if (realpathSync(git('rev-parse', '--show-toplevel')) !== realpathSync(root)) throw Error('Deployment tools must be in the repository root checkout');
  if (git('rev-parse', 'HEAD') !== sourceSHA) throw Error('Deployment checkout HEAD differs from release source SHA');
  if (git('status', '--porcelain', '--untracked-files=normal') !== '') throw Error('Production apply requires a clean Git worktree');
  return true;
}
