import { statfsSync } from 'node:fs';
import { resolve } from 'node:path';
export function checkDisk(path, minimumGiB = 10, minimumPercent = 15) {
  if (!Number.isFinite(minimumGiB) || minimumGiB < 1 || !Number.isFinite(minimumPercent) || minimumPercent < 5 || minimumPercent > 95) throw Error('Invalid disk thresholds');
  const data = statfsSync(resolve(path), { bigint: true });
  const available = data.bavail * data.bsize;
  if (available < BigInt(Math.ceil(minimumGiB * 1024 ** 3)) || data.bavail * 100n < data.blocks * BigInt(minimumPercent)) throw Error('Insufficient free disk: upgrade refused');
  return Number(available / 1024n ** 3n);
}
if (process.argv[1]?.endsWith('disk-check.mjs')) {
  try { console.log('DISK_GATE=PASS available_GiB=' + checkDisk(process.argv[2] || '/var/lib/docker', Number(process.env.CRANE_MIN_FREE_GIB || 10), Number(process.env.CRANE_MIN_FREE_PERCENT || 15))); }
  catch (error) { console.error(error.message); process.exitCode = 1; }
}
