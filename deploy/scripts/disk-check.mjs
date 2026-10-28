import { statfsSync, realpathSync, statSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { isAbsolute, resolve } from 'node:path';
export function checkDisk(path, minimumGiB = 10, minimumPercent = 15, statfs = statfsSync) {
  if (!Number.isFinite(minimumGiB) || minimumGiB < 10 || !Number.isInteger(minimumPercent) || minimumPercent < 15 || minimumPercent > 95) throw Error('Disk thresholds must be at least 10 GiB and 15 percent');
  const data = statfs(resolve(path), { bigint: true });
  const available = data.bavail * data.bsize;
  if (data.blocks <= 0n || data.bsize <= 0n) throw Error('Cannot confirm filesystem capacity');
  if (available < BigInt(Math.ceil(minimumGiB * 1024 ** 3)) || data.bavail * 100n < data.blocks * BigInt(minimumPercent)) throw Error('Insufficient free disk: upgrade refused');
  return Number(available / 1024n ** 3n);
}
export function checkProductionDisks({ backupRoot, expectedDockerRoot, docker = 'docker' }, { execute = spawnSync, canonical = realpathSync, statfs = statfsSync, directory = path => statSync(path).isDirectory() } = {}) {
  const result = execute(docker, ['info', '--format', '{{.DockerRootDir}}'], { encoding: 'utf8' });
  const reported = result.stdout?.trim();
  if (result.error || result.status !== 0 || !reported || !isAbsolute(reported) || /[\r\n]/.test(reported)) throw Error('Cannot confirm actual DockerRootDir');
  // statfs runs on this host. A remote daemon's same-named path is not its filesystem.
  const local = endpoint => /^(unix:\/\/|npipe:\/\/)/.test(endpoint || '');
  const context = execute(docker, ['context', 'inspect', '--format', '{{.Endpoints.docker.Host}}'], { encoding: 'utf8' });
  if (context.error || context.status !== 0 || !local(context.stdout?.trim()) || process.env.DOCKER_HOST && !local(process.env.DOCKER_HOST)) throw Error('Disk preflight requires a confirmed local Docker daemon');
  const dockerRoot = canonical(reported);
  if (expectedDockerRoot && (!isAbsolute(expectedDockerRoot) || canonical(expectedDockerRoot) !== dockerRoot)) throw Error('Configured Docker root differs from daemon DockerRootDir');
  if (!backupRoot || !isAbsolute(backupRoot)) throw Error('Explicit absolute Crane backup root required');
  const backup = canonical(backupRoot); // Must exist; never silently inspect an ancestor filesystem.
  if (!directory(dockerRoot) || !directory(backup)) throw Error('Disk roots must be existing directories');
  const dockerFreeGiB = checkDisk(dockerRoot, 10, 15, statfs);
  const backupFreeGiB = checkDisk(backup, 10, 15, statfs);
  return { dockerRoot, backupRoot: backup, dockerFreeGiB, backupFreeGiB };
}
if (import.meta.main) {
  try {
    if (!process.argv[2] || !isAbsolute(process.argv[2])) throw Error('Explicit filesystem path required');
    console.log('DISK_GATE=PASS available_GiB=' + checkDisk(process.argv[2], Number(process.env.CRANE_MIN_FREE_GIB || 10), Number(process.env.CRANE_MIN_FREE_PERCENT || 15)));
  }
  catch (error) { console.error(error.message); process.exitCode = 1; }
}
