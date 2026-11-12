// R5-UX-PERF: Crane Web Console nginx compression and caching contract.
// Static part: the production config enables gzip only for static text, caches only
// content-hashed build output as immutable, keeps the security headers on every
// response and leaves API/auth to cloud-api. Live part (when an nginx binary is
// available: CRANE_NGINX_BIN or `nginx` on PATH): the same config is run against a
// fixture webroot and a stub upstream, and real response headers are asserted.
// The shared u-dao ingress (deploy/ingress) is out of scope and must stay untouched.
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, mkdtempSync, mkdirSync, writeFileSync, existsSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { spawn, spawnSync } from 'node:child_process';
import http from 'node:http';
import { gunzipSync } from 'node:zlib';

const conf = readFileSync('deploy/nginx/production.conf', 'utf8');
const code = conf.split('\n').map((l) => l.replace(/#.*$/, '')).join('\n');
const server = code.slice(code.indexOf('server {'));
const apiBlock = code.slice(code.indexOf('location ~ ^/(api|auth)/'), code.indexOf('location /assets/'));

test('gzip 仅对静态文本启用，Vary 区分编码', () => {
  assert.match(server, /^\s*gzip on;/m);
  assert.match(server, /^\s*gzip_vary on;/m);
  const types = server.match(/gzip_types ([^;]+);/)?.[1].split(/\s+/) ?? [];
  for (const t of ['text/css', 'application/javascript', 'text/javascript', 'application/json', 'image/svg+xml']) assert.ok(types.includes(t), `gzip_types must include ${t}`);
  for (const t of types) assert.doesNotMatch(t, /^(image\/(png|jpe?g|webp|gif)|font\/woff2|application\/(zip|gzip|octet-stream))$/, `already-compressed type ${t} must not be gzipped`);
  assert.match(apiBlock, /^\s*gzip off;/m, 'API/auth responses carry session secrets and are not compressed here');
});

test('仅内容哈希命名的构建产物使用一年 immutable；其余重新验证，API/认证不由本层设置', () => {
  const map = code.match(/map \$uri \$crane_cache_control \{([\s\S]*?)\n\s*\}/)?.[1];
  assert.ok(map, 'cache map present');
  const rows = map.trim().split('\n').map((l) => l.trim()).filter(Boolean);
  const immutable = rows.filter((r) => /immutable/.test(r));
  assert.equal(immutable.length, 1, 'exactly one immutable rule');
  assert.match(immutable[0], /\^\/assets\//, 'immutable is limited to /assets/');
  assert.match(immutable[0], /\{8\}/, 'immutable requires the Vite 8-character content hash');
  const rx = new RegExp(immutable[0].match(/"~([^"]+)"/)[1]);
  for (const p of ['/assets/index-Bf-x-aiC.js', '/assets/three-DGRkhwCV.js', '/assets/index-CWSQwUDw.css']) assert.ok(rx.test(p), `${p} is hashed`);
  for (const p of ['/index.html', '/', '/geo/geo-manifest.json', '/geo/china/100000.json', '/assets/logo.svg', '/assets/index.js', '/favicon.svg']) assert.ok(!rx.test(p), `${p} must not be immutable`);
  assert.match(rows.find((r) => /^"~\^\/\(api\|auth\)\/"/.test(r)) ?? '', /''/, 'API/auth get no Cache-Control from nginx');
  assert.match(rows.find((r) => r.startsWith('default')) ?? '', /'no-cache'/);
  // Server-level add_header so security headers are never dropped by a location-level add_header.
  const locations = server.split(/\n\s*location /).slice(1);
  for (const l of locations) assert.doesNotMatch(l, /add_header/, 'no location-level add_header (would drop CSP/nosniff)');
  assert.match(server, /add_header Cache-Control \$crane_cache_control always;/);
});

test('安全头与 API/认证代理保持不变', () => {
  for (const h of [
    "add_header X-Content-Type-Options nosniff always;",
    "add_header Referrer-Policy no-referrer always;",
    "add_header X-Frame-Options DENY always;",
    "add_header Permissions-Policy 'camera=(), microphone=(), geolocation=()' always;",
    `add_header Content-Security-Policy "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self'; worker-src 'self' blob:; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'" always;`,
  ])
    assert.ok(server.includes(h), `missing ${h}`);
  for (const d of ['access_log off;', 'proxy_pass http://cloud-api:8081;', 'proxy_buffering off;', 'proxy_read_timeout 65s;']) assert.ok(apiBlock.includes(d), `API block keeps ${d}`);
  assert.doesNotMatch(apiBlock, /proxy_hide_header|proxy_ignore_headers|proxy_cache/, 'upstream Cache-Control / Set-Cookie must pass through');
  assert.match(code, /location \/assets\/ \{ try_files \$uri =404; \}/, 'missing chunk is 404, not index.html');
  assert.match(code, /location \/ \{ try_files \$uri \$uri\/ \/index\.html; \}/, 'SPA fallback unchanged');
});

test('共享 Ingress 示例未被本轮修改（不启用压缩、不改缓存）', () => {
  for (const f of ['deploy/ingress/crane.youdaozd.com.conf.example', 'deploy/ingress/auth.crane.youdaozd.com.conf.example']) {
    const s = readFileSync(f, 'utf8');
    assert.doesNotMatch(s, /gzip|immutable/, `${f} must stay unchanged`);
  }
});

function nginxBin() {
  const env = process.env.CRANE_NGINX_BIN;
  if (env) return env;
  const r = spawnSync('sh', ['-c', 'command -v nginx'], { encoding: 'utf8' });
  return r.status === 0 ? r.stdout.trim() : null;
}
const bin = nginxBin();
function mimeTypes() {
  for (const p of [process.env.CRANE_NGINX_MIME, '/etc/nginx/mime.types', bin && join(bin, '../../etc/nginx/mime.types')]) if (p && existsSync(p)) return p;
  return null;
}

test('真实 nginx：压缩、Vary、缓存分层、条件请求与安全头', { skip: !bin && !process.env.CI && 'NOT_RUN: no nginx binary (set CRANE_NGINX_BIN)' }, async () => {
  assert.ok(bin, 'CI must run the live nginx contract (GitHub ubuntu runners ship nginx); never SKIP in CI');
  const dir = mkdtempSync(join(tmpdir(), 'crane-nginx-'));
  const root = join(dir, 'html');
  mkdirSync(join(root, 'assets'), { recursive: true });
  mkdirSync(join(root, 'geo/china'), { recursive: true });
  const big = (s) => s.repeat(4000);
  writeFileSync(join(root, 'index.html'), '<!doctype html><title>Crane</title>' + big('<!-- pad -->'));
  writeFileSync(join(root, 'assets/index-Bf-x-aiC.js'), big('console.log("crane");'));
  writeFileSync(join(root, 'assets/index-CWSQwUDw.css'), big('body{color:#fff}'));
  writeFileSync(join(root, 'assets/logo.js'), big('/*unhashed*/'));
  writeFileSync(join(root, 'geo/china/100000.json'), JSON.stringify({ type: 'FeatureCollection', features: [], pad: big('x') }));
  const upstream = http.createServer((req, res) => {
    res.writeHead(200, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store', 'Set-Cookie': 'crane_session=x; Path=/; HttpOnly; Secure; SameSite=Lax' });
    res.end(JSON.stringify({ path: req.url, pad: big('y') }));
  });
  await new Promise((r) => upstream.listen(0, '127.0.0.1', r));
  const up = upstream.address().port;
  const port = 20000 + Math.floor(Math.random() * 20000);
  const mime = mimeTypes();
  const local = conf
    .replace('include /etc/nginx/mime.types;', mime ? `include ${mime};` : 'types { text/html html; application/javascript js; text/css css; application/json json; }')
    .replace('/usr/share/nginx/html', root)
    .replace('http://cloud-api:8081', `http://127.0.0.1:${up}`)
    .replace('pid /tmp/nginx.pid', `pid ${dir}/nginx.pid`)
    .replace(/\/tmp\/(client_body|proxy|fastcgi|uwsgi|scgi)/g, `${dir}/$1`)
    .replace('listen 8080', `listen 127.0.0.1:${port}`)
    .replace('/dev/stderr', `${dir}/error.log`)
    .replace('/dev/stdout', `${dir}/access.log`);
  writeFileSync(join(dir, 'nginx.conf'), local);
  const t = spawnSync(bin, ['-t', '-p', dir + '/', '-c', join(dir, 'nginx.conf')], { encoding: 'utf8' });
  assert.equal(t.status, 0, t.stderr);
  const proc = spawn(bin, ['-p', dir + '/', '-c', join(dir, 'nginx.conf'), '-g', 'daemon off;'], { stdio: 'ignore' });
  try {
    const get = async (path, headers = {}) => {
      for (let i = 0; i < 50; i++) {
        try {
          return await new Promise((resolve, reject) => {
            http.get({ host: '127.0.0.1', port, path, headers }, (res) => {
              const chunks = [];
              res.on('data', (c) => chunks.push(c));
              res.on('end', () => resolve({ status: res.statusCode, headers: res.headers, body: Buffer.concat(chunks) }));
            }).on('error', reject);
          });
        } catch {
          await new Promise((r) => setTimeout(r, 100));
        }
      }
      throw Error('nginx did not start');
    };
    const gz = { 'Accept-Encoding': 'gzip' };
    const js = await get('/assets/index-Bf-x-aiC.js', gz);
    assert.equal(js.headers['content-encoding'], 'gzip');
    assert.match(js.headers.vary ?? '', /Accept-Encoding/);
    assert.equal(js.headers['cache-control'], 'public, max-age=31536000, immutable');
    assert.match(gunzipSync(js.body).toString(), /^console\.log/);
    assert.ok(js.body.length < 4000 * 21 / 10, 'compressed');
    const plain = await get('/assets/index-Bf-x-aiC.js');
    assert.equal(plain.headers['content-encoding'], undefined, 'identity for clients without gzip');
    const css = await get('/assets/index-CWSQwUDw.css', gz);
    assert.equal(css.headers['content-encoding'], 'gzip');
    assert.equal(css.headers['cache-control'], 'public, max-age=31536000, immutable');
    for (const p of ['/', '/index.html', '/overview/deep/link', '/assets/logo.js', '/geo/china/100000.json']) {
      const r = await get(p, gz);
      assert.equal(r.status, 200, p);
      assert.equal(r.headers['cache-control'], 'no-cache', `${p} revalidates`);
      assert.equal(r.headers['x-content-type-options'], 'nosniff', p);
      assert.match(r.headers['content-security-policy'] ?? '', /frame-ancestors 'none'/, p);
      assert.equal(r.headers['x-frame-options'], 'DENY', p);
    }
    assert.equal((await get('/geo/china/100000.json', gz)).headers['content-encoding'], 'gzip');
    const missing = await get('/assets/gone-AAAAAAAA.js', gz);
    assert.equal(missing.status, 404, 'missing chunk is not index.html');
    const etag = (await get('/index.html')).headers.etag;
    assert.ok(etag);
    assert.equal((await get('/index.html', { 'If-None-Match': etag })).status, 304);
    for (const p of ['/api/v1/projects', '/auth/session']) {
      const r = await get(p, gz);
      assert.equal(r.headers['cache-control'], 'no-store', `${p}: upstream no-store passes through, nothing added`);
      assert.equal(r.headers['content-encoding'], undefined, `${p}: dynamic secret-bearing response not compressed`);
      assert.match(String(r.headers['set-cookie']), /HttpOnly; Secure; SameSite=Lax/, `${p}: cookie attributes unchanged`);
      assert.equal(r.headers['x-content-type-options'], 'nosniff');
      assert.match(r.headers['content-security-policy'] ?? '', /default-src 'self'/);
    }
  } finally {
    proc.kill('SIGTERM');
    upstream.close();
  }
});
