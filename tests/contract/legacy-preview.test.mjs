import test from 'node:test';
import assert from 'node:assert/strict';
import { existsSync, readFileSync } from 'node:fs';

// D1 (2026-11-10): the separate b4 public Preview is cancelled. These tests pin what remains of it:
// the Preview candidate build path is retired (single build chain), the legacy precheck stays
// read-only and leak-free, and no Preview identifier can leak into the crane development stack.

test('旧 Preview 候选构建链已退役，只保留一条共享构建实现', () => {
  assert.equal(existsSync('.github/workflows/preview-candidate.yml'), false);
  assert.equal(existsSync('tools/preview-candidate.mjs'), false);
  assert.equal(existsSync('tools/uatdata/main_test.go.4931661366237939467'), false, 'stray test artefact must be removed');
});

test('G1 旧预检脚本严格只读、不落盘、不输出密钥哈希', () => {
  const sh = readFileSync('deploy/scripts/h3-preview-precheck.sh', 'utf8');
  const code = sh.split('\n').filter((l) => !/^\s*#/.test(l) && !/^\s*echo /.test(l)).join('\n');
  for (const verb of [/compose[^\n|]*\s(up|down|rm|restart|stop|start|create|pull|exec|run)\b/, /(docker|\$DOCKER) (rm|stop|kill|restart|run|exec|pull|push|network (create|rm|connect|disconnect)|volume (create|rm|prune)|system prune)/i,
    /systemctl (start|stop|restart|reload|enable|disable)/, /nginx -s/, /certbot/, /\bpsql\b/, /\bcat [^\n]*secrets/, />\s*\/(etc|srv)\//, /\btee\b/, /\bchmod\b|\bchown\b/])
    assert.ok(!verb.test(code), `precheck must not contain ${verb}`);
  // No persisted resolved configuration, no secret hash or hash fragment in output.
  assert.doesNotMatch(code, />\s*\/tmp\//, 'no file may be written under /tmp');
  assert.doesNotMatch(code, /sha256sum|md5sum|cut -c/, 'secret comparison must not print digests');
  assert.match(code, /cmp -s/);
  assert.match(sh, /SAME/); assert.match(sh, /DIFFERENT/); assert.match(sh, /NOT_ACCESSIBLE/);
  assert.match(code, /--resolved -/, 'resolved compose must be piped, not saved');
  assert.match(sh, /PRECHECK_MODE=READ_ONLY/);
  assert.match(sh, /PRECHECK_BLOCKERS=/);
});

test('旧 Preview 组件保留为停用状态且 fail-closed', () => {
  const target = readFileSync('apps/web-console/src/cloud/preview-target.ts', 'utf8');
  assert.match(target, /DEPRECATED/);
  assert.match(target, /PREVIEW_ORIGIN = 'https:\/\/b4\.crane\.youdaozd\.com'/, 'the guard must keep refusing crane.youdaozd.com');
  assert.match(readFileSync('deploy/compose/docker-compose.b4-preview.yml', 'utf8'), /DEPRECATED/);
  assert.match(readFileSync('tools/provision-keycloak/preview.go', 'utf8'), /DEPRECATED/);
  const env = readFileSync('deploy/compose/b4-preview.env.example', 'utf8');
  assert.doesNotMatch(env, /release-manifest\.json artifact/, 'stale production-release digest guidance removed');
  assert.match(env, /独立候选镜像/);
});
