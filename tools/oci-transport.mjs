// R7: offline transport material for a candidate, built from the ORIGINAL registry digests.
// `docker save` re-serializes layers and drops the registry manifest (RepoDigests empty, compressed
// layers re-created), which is how the earlier GHCR "missing compressed layer" mismatch arose. This
// tool copies each image by its exact manifest digest with skopeo into an OCI archive, preserving
// the manifest bytes, and verifies the archive before it is shipped.
//
//   node tools/oci-transport.mjs pack   <manifest.json> <out-dir>   (needs network + skopeo)
//   node tools/oci-transport.mjs verify <out-dir>                   (offline; on the target host)
//
// Output per image: <name>.oci.tar, <name>.oci.tar.sha256, plus transport.json (digest, platform,
// size, archive sha256). Import on the target: see docs/08-部署运维与成本/R7-模拟器发布与离线运输.md.
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { createReadStream, mkdirSync, readFileSync, statSync, writeFileSync, chmodSync } from 'node:fs';
import { join, resolve } from 'node:path';

const DIGEST = /^sha256:[a-f0-9]{64}$/;
const NAME = /^ghcr\.io\/guolichen007\/crane-operations-platform\/(?:dev\/)?(cloud-api|web-console|device-simulator)$/;

function skopeo(args) {
  const r = spawnSync('skopeo', args, { encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 });
  if (r.error || r.status !== 0) throw Error('skopeo failed: ' + args[0]);
  return r.stdout;
}
async function sha256File(path) {
  const h = createHash('sha256');
  for await (const chunk of createReadStream(path)) h.update(chunk);
  return h.digest('hex');
}
export const sha256 = (b) => 'sha256:' + createHash('sha256').update(b).digest('hex');

/** Images from a dev-manifest.json or simulator-manifest.json, strictly validated. */
export function imagesOf(manifest) {
  if (manifest.schema_version !== 1 || !/^[a-f0-9]{40}$/.test(manifest.source_sha || '') || !Array.isArray(manifest.images) || !manifest.images.length) throw Error('Invalid candidate manifest');
  return manifest.images.map((i) => {
    if (!NAME.test(i.image_name) || !DIGEST.test(i.image_digest) || i.source_sha !== manifest.source_sha) throw Error('Invalid image entry');
    return { name: NAME.exec(i.image_name)[1], ref: `${i.image_name}@${i.image_digest}`, digest: i.image_digest, image_name: i.image_name };
  });
}

/** The raw manifest bytes must hash to the digest; an index must list the expected platform. */
export function checkManifest(raw, digest, platform = { os: 'linux', architecture: 'amd64' }) {
  if (sha256(raw) !== digest) throw Error('manifest bytes do not match digest');
  const m = JSON.parse(raw);
  const index = Array.isArray(m.manifests);
  if (index) {
    const hit = m.manifests.find((x) => x.platform?.os === platform.os && x.platform?.architecture === platform.architecture);
    if (!hit) throw Error(`index has no ${platform.os}/${platform.architecture} manifest`);
    return { kind: 'index', platformDigest: hit.digest, platforms: m.manifests.filter((x) => x.platform && x.platform.os !== 'unknown').map((x) => `${x.platform.os}/${x.platform.architecture}`) };
  }
  return { kind: 'manifest', platformDigest: digest, platforms: [] };
}

async function pack(manifestPath, out) {
  const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'));
  mkdirSync(out, { recursive: true, mode: 0o700 });
  const entries = [];
  for (const img of imagesOf(manifest)) {
    const raw = skopeo(['inspect', '--raw', `docker://${img.ref}`]);
    const info = checkManifest(raw, img.digest);
    const archive = join(out, `${img.name}.oci.tar`);
    // --preserve-digests refuses any re-compression: the archive holds exactly the registry blobs.
    skopeo(['copy', '--all', '--preserve-digests', `docker://${img.ref}`, `oci-archive:${archive}:${img.image_name.split('/').pop()}@${img.digest}`]);
    const archiveSha = await sha256File(archive);
    writeFileSync(archive + '.sha256', `${archiveSha}  ${img.name}.oci.tar\n`);
    const inside = skopeo(['inspect', '--raw', `oci-archive:${archive}`]);
    if (sha256(inside) !== img.digest) throw Error(`${img.name}: archive manifest digest differs from the registry digest`);
    entries.push({ name: img.name, image: img.image_name, digest: img.digest, kind: info.kind, platforms: info.platforms, archive: `${img.name}.oci.tar`, archive_sha256: archiveSha, archive_bytes: statSync(archive).size });
  }
  const t = { schema_version: 1, kind: 'crane-oci-transport', source_sha: manifest.source_sha, manifest_kind: manifest.kind, created_at: new Date().toISOString(), images: entries };
  writeFileSync(join(out, 'transport.json'), JSON.stringify(t, null, 2) + '\n');
  chmodSync(out, 0o700);
  console.log('OCI_TRANSPORT_PACK=PASS ' + entries.map((e) => `${e.name}=${e.digest} ${e.archive_bytes}B`).join(' '));
}

async function verify(out) {
  const t = JSON.parse(readFileSync(join(out, 'transport.json'), 'utf8'));
  if (t.kind !== 'crane-oci-transport' || !/^[a-f0-9]{40}$/.test(t.source_sha)) throw Error('Invalid transport.json');
  for (const e of t.images) {
    const archive = join(out, e.archive);
    const actual = await sha256File(archive);
    if (actual !== e.archive_sha256) throw Error(`${e.name}: archive sha256 mismatch`);
    const inside = skopeo(['inspect', '--raw', `oci-archive:${archive}`]);
    if (sha256(inside) !== e.digest) throw Error(`${e.name}: manifest digest mismatch`);
    const cfg = JSON.parse(skopeo(['inspect', '--config', `oci-archive:${archive}`]));
    if (cfg.config?.Labels?.['org.opencontainers.image.revision'] !== t.source_sha) throw Error(`${e.name}: revision label is not ${t.source_sha}`);
    console.log(`OCI_TRANSPORT_VERIFY ${e.name} digest=${e.digest} platform=${cfg.os}/${cfg.architecture} bytes=${e.archive_bytes}`);
  }
  console.log('OCI_TRANSPORT_VERIFY=PASS source_sha=' + t.source_sha);
}

if (import.meta.main) {
  const [op, a, b] = process.argv.slice(2);
  try {
    if (op === 'pack' && a && b) await pack(resolve(a), resolve(b));
    else if (op === 'verify' && a) await verify(resolve(a));
    else throw Error('usage: oci-transport.mjs pack <manifest.json> <out-dir> | verify <out-dir>');
  } catch (e) { console.error(e.message); process.exitCode = 1; }
}
